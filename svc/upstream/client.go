package upstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
)

const (
	// IMAGE_GENERATION_TIMEOUT matches Grok Build's total Imagine request timeout.
	IMAGE_GENERATION_TIMEOUT = 300 * time.Second
	// IMAGE_GENERATION_READ_TIMEOUT allows buffered image generation to wait for response headers.
	IMAGE_GENERATION_READ_TIMEOUT = 240 * time.Second
)

// Client sends sanitized, context-bound requests to concrete provider profiles.
type Client struct {
	httpClient         *http.Client
	imageHTTPClient    *http.Client
	messagesTimeout    time.Duration
	streamTimeout      time.Duration
	countTokensTimeout time.Duration
}

// NewClient clones an injected HTTP client and applies proxy timeout policy.
func NewClient(httpClient *http.Client, cfg TimeoutConfig) (*Client, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("upstream client: nil HTTP client")
	}
	if cfg.MessagesMs <= 0 {
		return nil, fmt.Errorf("upstream client: messages timeout must be positive")
	}
	if cfg.StreamMessagesMs <= 0 {
		return nil, fmt.Errorf("upstream client: stream messages timeout must be positive")
	}
	if cfg.CountTokensMs <= 0 {
		return nil, fmt.Errorf("upstream client: count tokens timeout must be positive")
	}

	clone := *httpClient
	clone.Timeout = 0
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	imageClone := clone
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if source, ok := transport.(*http.Transport); ok {
		transportClone := source.Clone()
		transportClone.ResponseHeaderTimeout = time.Duration(cfg.MessagesMs) * time.Millisecond
		clone.Transport = transportClone
		imageTransportClone := source.Clone()
		imageTransportClone.ResponseHeaderTimeout = IMAGE_GENERATION_READ_TIMEOUT
		imageClone.Transport = imageTransportClone
	} else {
		clone.Transport = transport
		imageClone.Transport = transport
	}

	return &Client{
		httpClient:         &clone,
		imageHTTPClient:    &imageClone,
		messagesTimeout:    time.Duration(cfg.MessagesMs) * time.Millisecond,
		streamTimeout:      time.Duration(cfg.StreamMessagesMs) * time.Millisecond,
		countTokensTimeout: time.Duration(cfg.CountTokensMs) * time.Millisecond,
	}, nil
}

// Do sends one model request to the endpoint selected by the target format.
func (c *Client) Do(ctx context.Context, profile Profile, cred *authmodel.Credential, envelope model.RequestEnvelope) (*http.Response, error) {
	endpoint, err := profile.ResolveGenerationEndpoint(envelope.TargetFormat, envelope.Stream)
	if err != nil {
		return nil, err
	}
	envelope, err = c.applyCredentialBody(ctx, profile, cred, envelope)
	if err != nil {
		return nil, err
	}
	timeout := c.messagesTimeout
	if envelope.Stream {
		timeout = c.streamTimeout
	}
	return c.do(ctx, profile, cred, envelope, endpoint, timeout, envelope.Stream)
}

// applyCredentialBody runs the profile's credential-aware body hook, for
// providers whose wire format carries account state in the body rather than in
// a header. Most profiles declare none and pass straight through.
func (c *Client) applyCredentialBody(
	ctx context.Context,
	profile Profile,
	cred *authmodel.Credential,
	envelope model.RequestEnvelope,
) (model.RequestEnvelope, error) {
	if profile.ApplyCredentialBody == nil {
		return envelope, nil
	}
	// The hook reads the credential, so it must not run on one that failed
	// validation — doWithHTTPClient's own check comes too late.
	if err := validateCredentialForProfile(profile, cred); err != nil {
		return model.RequestEnvelope{}, err
	}
	body, err := profile.ApplyCredentialBody(ctx, cred, envelope.Body)
	if err != nil {
		return model.RequestEnvelope{}, err
	}
	envelope.Body = body
	return envelope, nil
}

// CountTokens sends one request to a profile's native token-count endpoint.
func (c *Client) CountTokens(ctx context.Context, profile Profile, cred *authmodel.Credential, envelope model.RequestEnvelope) (*http.Response, error) {
	if strings.TrimSpace(profile.CountTokensEndpoint) == "" {
		return nil, &model.ProxyError{
			Kind:    model.ERROR_UNSUPPORTED_FEATURE,
			Status:  http.StatusNotImplemented,
			Code:    "token_count_unsupported",
			Message: fmt.Sprintf("profile %q does not support native token counting", profile.ID),
		}
	}
	return c.do(ctx, profile, cred, envelope, profile.CountTokensEndpoint, c.countTokensTimeout, false)
}

// GenerateImage sends an OpenAI-compatible image-generation request to the
// image endpoint declared by the selected profile. OAuth credentials
// intentionally bypass provider inference base URLs when the profile's image
// endpoint is public (for example, xAI Imagine).
func (c *Client) GenerateImage(
	ctx context.Context,
	profile Profile,
	cred *authmodel.Credential,
	envelope model.RequestEnvelope,
) (*http.Response, error) {
	if strings.TrimSpace(profile.ImageGenerationBaseURL) == "" ||
		strings.TrimSpace(profile.ImageGenerationEndpoint) == "" {
		return nil, &model.ProxyError{
			Kind:    model.ERROR_UNSUPPORTED_FEATURE,
			Status:  http.StatusNotImplemented,
			Code:    "image_generation_unsupported",
			Message: fmt.Sprintf("profile %q does not support image generation", profile.ID),
		}
	}
	if err := validateCredentialForProfile(profile, cred); err != nil {
		return nil, err
	}

	imageProfile := profile
	imageProfile.BaseURL = profile.ImageGenerationBaseURL
	imageCredential := *cred
	if imageCredential.Kind == authmodel.KIND_OAUTH {
		imageCredential.BaseURL = ""
	}
	var imageHTTPClient *http.Client
	if c != nil {
		imageHTTPClient = c.imageHTTPClient
	}
	return c.doWithHTTPClient(
		imageHTTPClient,
		ctx,
		imageProfile,
		&imageCredential,
		envelope,
		profile.ImageGenerationEndpoint,
		IMAGE_GENERATION_TIMEOUT,
		false,
		true,
	)
}

// EditImage sends an OpenAI-compatible multipart image-edit request to the
// image-edit endpoint declared by the selected profile. The multipart body is
// preserved byte-for-byte so boundaries and uploaded image bytes remain intact.
func (c *Client) EditImage(
	ctx context.Context,
	profile Profile,
	cred *authmodel.Credential,
	envelope model.RequestEnvelope,
) (*http.Response, error) {
	if strings.TrimSpace(profile.ImageEditBaseURL) == "" ||
		strings.TrimSpace(profile.ImageEditEndpoint) == "" {
		return nil, &model.ProxyError{
			Kind:    model.ERROR_UNSUPPORTED_FEATURE,
			Status:  http.StatusNotImplemented,
			Code:    "image_edit_unsupported",
			Message: fmt.Sprintf("profile %q does not support image editing", profile.ID),
		}
	}
	if err := validateCredentialForProfile(profile, cred); err != nil {
		return nil, err
	}

	imageProfile := profile
	imageProfile.BaseURL = profile.ImageEditBaseURL
	imageCredential := *cred
	if imageCredential.Kind == authmodel.KIND_OAUTH {
		imageCredential.BaseURL = ""
	}
	var imageHTTPClient *http.Client
	if c != nil {
		imageHTTPClient = c.imageHTTPClient
	}
	return c.doWithHTTPClient(
		imageHTTPClient,
		ctx,
		imageProfile,
		&imageCredential,
		envelope,
		profile.ImageEditEndpoint,
		IMAGE_GENERATION_TIMEOUT,
		false,
		true,
	)
}

func (c *Client) do(
	ctx context.Context,
	profile Profile,
	cred *authmodel.Credential,
	envelope model.RequestEnvelope,
	endpoint string,
	timeout time.Duration,
	stream bool,
) (*http.Response, error) {
	var httpClient *http.Client
	if c != nil {
		httpClient = c.httpClient
	}
	return c.doWithHTTPClient(httpClient, ctx, profile, cred, envelope, endpoint, timeout, stream, false)
}

func (c *Client) doWithHTTPClient(
	httpClient *http.Client,
	ctx context.Context,
	profile Profile,
	cred *authmodel.Credential,
	envelope model.RequestEnvelope,
	endpoint string,
	timeout time.Duration,
	stream bool,
	imageGeneration bool,
) (*http.Response, error) {
	if c == nil || httpClient == nil {
		return nil, unavailableUpstreamError("upstream HTTP client is unavailable", nil)
	}
	if ctx == nil {
		return nil, unavailableUpstreamError("request context is nil", nil)
	}
	if err := validateCredentialForProfile(profile, cred); err != nil {
		return nil, err
	}

	baseURL := profile.BaseURL
	if strings.TrimSpace(cred.BaseURL) != "" {
		baseURL = cred.BaseURL
	}
	requestURL, err := buildEndpointURL(baseURL, endpoint)
	if err != nil {
		return nil, unavailableUpstreamError("invalid upstream endpoint", err)
	}

	requestContext, cancel := context.WithTimeout(ctx, timeout)
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, requestURL, bytes.NewReader(envelope.Body))
	if err != nil {
		cancel()
		return nil, unavailableUpstreamError("create upstream request", err)
	}
	contentType := strings.TrimSpace(envelope.ContentType)
	if contentType == "" {
		contentType = "application/json"
	}
	request.Header.Set("Content-Type", contentType)
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	surface := SURFACE_INFERENCE
	if imageGeneration {
		surface = SURFACE_IMAGE
	}
	forwardAllowlistedHeaders(profile, envelope.Headers, request.Header)
	applyCredentialHeader(profile, cred, request.Header)
	if profile.ApplyIdentityHeaders != nil {
		profile.ApplyIdentityHeaders(IdentityRequest{
			Credential: cred,
			Envelope:   envelope,
			Header:     request.Header,
			Surface:    surface,
		})
	}

	response, err := httpClient.Do(request)
	if err != nil {
		cancel()
		return nil, transportProxyError(err)
	}
	response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func buildEndpointURL(baseURL, endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Host == "" {
		return "", fmt.Errorf("base URL must be absolute and include a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("base URL must not include userinfo")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("base URL must not include query or fragment")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && !(scheme == "http" && loopbackHost(parsed.Hostname())) {
		return "", fmt.Errorf("base URL must use HTTPS or loopback HTTP")
	}
	if !strings.HasPrefix(endpoint, "/") || strings.Contains(endpoint, "#") {
		return "", fmt.Errorf("endpoint must be a fixed absolute path")
	}
	// Endpoints are profile constants, never request-derived, so a fixed query
	// string (Gemini's alt=sse switch) is safe as long as it parses.
	path, rawQuery, _ := strings.Cut(endpoint, "?")
	if rawQuery != "" {
		if _, err := url.ParseQuery(rawQuery); err != nil {
			return "", fmt.Errorf("endpoint query is malformed: %w", err)
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = rawQuery
	return parsed.String(), nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateCredentialForProfile(profile Profile, cred *authmodel.Credential) error {
	if cred == nil {
		return authProxyError("upstream credential is nil", nil)
	}
	if err := cred.Validate(); err != nil {
		return authProxyError("upstream credential is invalid", err)
	}
	if !strings.EqualFold(strings.TrimSpace(profile.CredentialProvider), strings.TrimSpace(cred.Provider)) {
		return authProxyError(fmt.Sprintf("credential provider %q does not match profile %q", cred.Provider, profile.ID), nil)
	}
	return nil
}

func forwardAllowlistedHeaders(profile Profile, source, target http.Header) {
	for name, values := range source {
		if !profile.AllowsRequestHeader(name) {
			continue
		}
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

// applyCredentialHeader puts the credential secret in the header the profile
// declares. This is the only auth the transport itself performs — anything a
// gateway additionally demands is client identity and belongs to the
// profile's ApplyIdentityHeaders hook.
func applyCredentialHeader(profile Profile, cred *authmodel.Credential, header http.Header) {
	secret := cred.APIKey
	scheme := profile.AuthScheme
	if cred.Kind == authmodel.KIND_OAUTH {
		secret = cred.AccessToken
		if profile.OAuthScheme != "" {
			scheme = profile.OAuthScheme
		}
	}
	switch scheme {
	case AUTH_X_API_KEY:
		header.Set("x-api-key", secret)
	case AUTH_BEARER:
		header.Set("Authorization", "Bearer "+secret)
	}
}

func firstNonBlankHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func ensureCommaSeparatedHeader(header http.Header, name, required string) {
	values := header.Values(name)
	items := make([]string, 0, len(values)+1)
	seen := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			key := strings.ToLower(item)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, item)
		}
	}
	key := strings.ToLower(required)
	if _, exists := seen[key]; !exists {
		items = append(items, required)
	}
	header.Set(name, strings.Join(items, ","))
}

func transportProxyError(err error) error {
	kind := model.ERROR_UPSTREAM
	status := http.StatusBadGateway
	code := "upstream_error"
	message := "upstream request failed"
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		kind = model.ERROR_TIMEOUT
		status = http.StatusGatewayTimeout
		code = "upstream_timeout"
		message = "upstream request timed out"
	}
	return &model.ProxyError{
		Kind:    kind,
		Status:  status,
		Code:    code,
		Message: message,
		Cause:   err,
	}
}

func unavailableUpstreamError(message string, cause error) error {
	return &model.ProxyError{
		Kind:    model.ERROR_UNAVAILABLE,
		Status:  http.StatusServiceUnavailable,
		Code:    "upstream_unavailable",
		Message: message,
		Cause:   cause,
	}
}

func authProxyError(message string, cause error) error {
	return &model.ProxyError{
		Kind:    model.ERROR_AUTH,
		Status:  http.StatusUnauthorized,
		Code:    "upstream_auth",
		Message: message,
		Cause:   cause,
	}
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}
