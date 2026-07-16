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

	"github.com/bizshuk/agentsdk/auth"
	"github.com/bizshuk/agentsdk/config"
	"github.com/bizshuk/proxy/protocol"
)

// Client sends sanitized, context-bound requests to concrete provider profiles.
type Client struct {
	httpClient         *http.Client
	messagesTimeout    time.Duration
	streamTimeout      time.Duration
	countTokensTimeout time.Duration
}

// NewClient clones an injected HTTP client and applies proxy timeout policy.
func NewClient(httpClient *http.Client, cfg config.ProxyTimeoutConfig) (*Client, error) {
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
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if source, ok := transport.(*http.Transport); ok {
		transportClone := source.Clone()
		transportClone.ResponseHeaderTimeout = time.Duration(cfg.MessagesMs) * time.Millisecond
		clone.Transport = transportClone
	} else {
		clone.Transport = transport
	}

	return &Client{
		httpClient:         &clone,
		messagesTimeout:    time.Duration(cfg.MessagesMs) * time.Millisecond,
		streamTimeout:      time.Duration(cfg.StreamMessagesMs) * time.Millisecond,
		countTokensTimeout: time.Duration(cfg.CountTokensMs) * time.Millisecond,
	}, nil
}

// Do sends one model request to the endpoint selected by the target format.
func (c *Client) Do(ctx context.Context, profile Profile, cred *auth.Credential, envelope protocol.RequestEnvelope) (*http.Response, error) {
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
func (c *Client) CountTokens(ctx context.Context, profile Profile, cred *auth.Credential, envelope protocol.RequestEnvelope) (*http.Response, error) {
	if strings.TrimSpace(profile.CountTokensEndpoint) == "" {
		return nil, &protocol.ProxyError{
			Kind:    protocol.ERROR_UNSUPPORTED_FEATURE,
			Status:  http.StatusNotImplemented,
			Code:    "token_count_unsupported",
			Message: fmt.Sprintf("profile %q does not support native token counting", profile.ID),
		}
	}
	return c.do(ctx, profile, cred, envelope, profile.CountTokensEndpoint, c.countTokensTimeout, false)
}

func (c *Client) do(
	ctx context.Context,
	profile Profile,
	cred *auth.Credential,
	envelope protocol.RequestEnvelope,
	endpoint string,
	timeout time.Duration,
	stream bool,
) (*http.Response, error) {
	if c == nil || c.httpClient == nil {
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

	response, err := c.httpClient.Do(request)
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

func validateCredentialForProfile(profile Profile, cred *auth.Credential) error {
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

func applyProviderHeaders(profile Profile, cred *auth.Credential, header http.Header) {
	secret := cred.APIKey
	if cred.Kind == auth.KIND_OAUTH {
		secret = cred.AccessToken
	}

	if strings.EqualFold(profile.CredentialProvider, "anthropic") && cred.Kind == auth.KIND_OAUTH {
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
	return fmt.Sprintf("%s/%s (%s; %s)", DEFAULT_CODEX_ORIGINATOR, DEFAULT_CODEX_VERSION, platform, architecture)
}

func transportProxyError(err error) error {
	kind := protocol.ERROR_UPSTREAM
	status := http.StatusBadGateway
	code := "upstream_error"
	message := "upstream request failed"
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		kind = protocol.ERROR_TIMEOUT
		status = http.StatusGatewayTimeout
		code = "upstream_timeout"
		message = "upstream request timed out"
	}
	return &protocol.ProxyError{
		Kind:    kind,
		Status:  status,
		Code:    code,
		Message: message,
		Cause:   err,
	}
}

func unavailableUpstreamError(message string, cause error) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNAVAILABLE,
		Status:  http.StatusServiceUnavailable,
		Code:    "upstream_unavailable",
		Message: message,
		Cause:   cause,
	}
}

func authProxyError(message string, cause error) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_AUTH,
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
