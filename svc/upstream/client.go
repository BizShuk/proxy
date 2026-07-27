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
	"runtime"
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
	endpoint, err := profile.ResolveEndpoint(envelope.TargetFormat)
	if err != nil {
		return nil, err
	}
	timeout := c.messagesTimeout
	if envelope.Stream {
		timeout = c.streamTimeout
	}
	return c.do(ctx, profile, cred, envelope, endpoint, timeout, envelope.Stream)
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
	request.Header.Set("Content-Type", "application/json")
	if stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	forwardAllowlistedHeaders(profile, envelope.Headers, request.Header)
	applyProviderHeaders(profile, cred, request.Header)
	if imageGeneration {
		applyImageGenerationHeaders(profile, request.Header)
	} else {
		applyXAIGrokOAuthHeaders(profile, cred, envelope, request.Header)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		cancel()
		return nil, transportProxyError(err)
	}
	response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func applyImageGenerationHeaders(profile Profile, header http.Header) {
	if !strings.EqualFold(profile.CredentialProvider, "xai") {
		return
	}
	header.Set("x-grok-client-version", DEFAULT_XAI_GROK_CLIENT_VERSION)
	header.Set("x-grok-client-identifier", DEFAULT_XAI_GROK_CLIENT_IDENTIFIER)
	header.Set("User-Agent", xaiGrokUserAgent())
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
	if !strings.HasPrefix(endpoint, "/") || strings.ContainsAny(endpoint, "?#") {
		return "", fmt.Errorf("endpoint must be a fixed absolute path")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + strings.TrimLeft(endpoint, "/")
	parsed.RawPath = ""
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

func applyProviderHeaders(profile Profile, cred *authmodel.Credential, header http.Header) {
	secret := cred.APIKey
	if cred.Kind == authmodel.KIND_OAUTH {
		secret = cred.AccessToken
	}

	if strings.EqualFold(profile.CredentialProvider, "anthropic") && cred.Kind == authmodel.KIND_OAUTH {
		header.Set("Authorization", "Bearer "+secret)
		header.Set("anthropic-dangerous-direct-browser-access", "true")
		ensureCommaSeparatedHeader(header, "anthropic-beta", ANTHROPIC_OAUTH_BETA)
	} else {
		switch profile.AuthScheme {
		case AUTH_X_API_KEY:
			header.Set("x-api-key", secret)
		case AUTH_BEARER:
			header.Set("Authorization", "Bearer "+secret)
		}
	}

	if profile.AnthropicVersion != "" {
		header.Set("anthropic-version", profile.AnthropicVersion)
	}
	if profile.ID == "openai-codex-oauth" {
		header.Set("originator", DEFAULT_CODEX_ORIGINATOR)
		header.Set("version", DEFAULT_CODEX_VERSION)
		header.Set("User-Agent", codexUserAgent())
		if strings.TrimSpace(cred.AccountID) != "" {
			header.Set("ChatGPT-Account-ID", cred.AccountID)
		}
	}
}

func applyXAIGrokOAuthHeaders(profile Profile, cred *authmodel.Credential, envelope model.RequestEnvelope, header http.Header) {
	if profile.ID != XAI_GROK_OAUTH_PROFILE_ID {
		return
	}

	header.Set(XAI_GROK_TOKEN_AUTH_HEADER, XAI_GROK_TOKEN_AUTH_VALUE)
	header.Set(XAI_GROK_AUTHENTICATE_RESPONSE_HEADER, XAI_GROK_AUTHENTICATE_RESPONSE_VALUE)
	header.Set("x-grok-client-version", DEFAULT_XAI_GROK_CLIENT_VERSION)
	header.Set("x-grok-client-identifier", DEFAULT_XAI_GROK_CLIENT_IDENTIFIER)
	header.Set("x-grok-client-mode", DEFAULT_XAI_GROK_CLIENT_MODE)
	header.Set("User-Agent", xaiGrokUserAgent())
	header.Set("x-grok-model-override", strings.TrimSpace(envelope.Model))

	requestID := firstNonBlankHeader(header, "x-grok-req-id", "x-request-id")
	if requestID != "" {
		header.Set("x-grok-req-id", requestID)
	}
	conversationID := firstNonBlankHeader(header, "x-grok-conv-id")
	if conversationID == "" {
		conversationID = requestID
	}
	if conversationID != "" {
		header.Set("x-grok-conv-id", conversationID)
	}
	sessionID := firstNonBlankHeader(header, "x-grok-session-id")
	if sessionID == "" {
		sessionID = conversationID
	}
	if sessionID != "" {
		header.Set("x-grok-session-id", sessionID)
	}
	if strings.TrimSpace(header.Get("x-grok-agent-id")) == "" {
		header.Set("x-grok-agent-id", DEFAULT_XAI_GROK_CLIENT_IDENTIFIER)
	}

	header.Del("x-grok-user-id")
	if strings.TrimSpace(cred.AccountID) != "" {
		header.Set("x-grok-user-id", strings.TrimSpace(cred.AccountID))
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

func codexUserAgent() string {
	platform, architecture := userAgentPlatform()
	return fmt.Sprintf("%s/%s (%s; %s)", DEFAULT_CODEX_ORIGINATOR, DEFAULT_CODEX_VERSION, platform, architecture)
}

func xaiGrokUserAgent() string {
	platform, architecture := userAgentPlatform()
	return fmt.Sprintf("%s/%s (%s; %s)", DEFAULT_XAI_GROK_CLIENT_IDENTIFIER, DEFAULT_XAI_GROK_CLIENT_VERSION, platform, architecture)
}

func userAgentPlatform() (string, string) {
	platform := "linux"
	switch runtime.GOOS {
	case "darwin":
		platform = "macos"
	case "windows":
		platform = "windows"
	}
	architecture := "x86_64"
	if runtime.GOARCH == "arm64" {
		architecture = "arm64"
	}
	return platform, architecture
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
