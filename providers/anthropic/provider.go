// Package anthropic adapts the official anthropic-sdk-go to agentsdk's
// core.Provider interface. The adapter is intentionally thin — it owns
// the auth token + model selection and translates core.Message ⇄
// anthropic.MessageParam in both directions.
//
// File layout:
//
//   - provider.go    — entry point, Provider struct, interface methods
//   - options.go     — functional options for New
//   - dto.go         — wire-format types (RequestBody, ContentBlock, ...)
//   - validate.go    — RequestBody.Validate()
//   - translate.go   — core.Message ⇄ Anthropic wire conversion
//   - auth_api.go    — ResolveAPIKey / IsOAuth
//   - auth_oauth.go  — OAuth device flow + PKCE helpers
//   - stream.go      — SSE parser → core.ModelChunk
//   - models.go      — DefaultCatalog
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/bizshuk/agentsdk/core"
)

// Provider implements core.Provider against the Anthropic API.
type Provider struct {
	client   *anthropic.Client
	model    anthropic.Model
	auth     core.Auth // resolved at construction; honors req.Auth overrides per call
	httpDoer *http.Client
	endpoint string
	apiVer   string
}

// New returns a Provider using an API key (or ANTHROPIC_API_KEY env
// fallback). model defaults to claude-3-5-sonnet-latest.
func New(opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	key := ResolveAPIKey(cfg.apiKey)
	if key == "" {
		return nil, fmt.Errorf("anthropic: API key not set (use WithAPIKey, WithAuth, or ANTHROPIC_API_KEY)")
	}
	clientOpts := []option.RequestOption{option.WithAPIKey(key)}
	if cfg.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(cfg.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)
	return &Provider{
		client:   &client,
		model:    anthropic.Model(cfg.model),
		auth:     core.Auth{APIKey: key, BaseURL: cfg.baseURL},
		httpDoer: http.DefaultClient,
		endpoint: resolveEndpoint(cfg.baseURL),
		apiVer:   "2023-06-01",
	}, nil
}

// NewWithOAuth constructs a provider from an OAuth credential.
func NewWithOAuth(token OAuthCredentials, opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if token.AccessToken == "" {
		return nil, fmt.Errorf("anthropic: OAuth access token is empty")
	}
	clientOpts := []option.RequestOption{option.WithAuthToken(token.AccessToken)}
	if cfg.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(cfg.baseURL))
	}
	client := anthropic.NewClient(clientOpts...)
	return &Provider{
		client:   &client,
		model:    anthropic.Model(cfg.model),
		auth:     core.Auth{Bearer: token.AccessToken, BaseURL: cfg.baseURL, Headers: map[string]string{OAuthBetaHeader: "true"}},
		httpDoer: http.DefaultClient,
		endpoint: resolveEndpoint(cfg.baseURL),
		apiVer:   "2023-06-01",
	}, nil
}

// ID implements core.Provider. Returns the family alone — "anthropic".
func (p *Provider) ID() string { return "anthropic" }

// Name is a convenience accessor returning "anthropic:<model>".
func (p *Provider) Name() string { return "anthropic:" + string(p.model) }

// Models implements core.Provider. Returns the static catalog.
func (p *Provider) Models() []core.ModelSpec { return DefaultCatalog() }

// AuthSchemes implements core.Provider. Anthropic accepts long-lived API
// keys AND OAuth access tokens (Claude Pro/Max).
func (p *Provider) AuthSchemes() []string {
	return []string{"api_key", "oauth"}
}

// Generate implements core.Provider.
func (p *Provider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	body := p.buildRequestBody(req)
	if err := body.Validate(); err != nil {
		return core.ModelResult{}, err
	}
	params, err := toSDKParams(body)
	if err != nil {
		return core.ModelResult{}, err
	}
	opts := p.authOptions(req)
	resp, err := p.client.Messages.New(ctx, params, opts...)
	if err != nil {
		return core.ModelResult{}, err
	}
	return fromSDKResponse(resp), nil
}

// Stream implements core.Provider. We go through a direct HTTP request
// (rather than the SDK's NewStreaming) so the SSE parser in stream.go
// owns the wire format and stays independently testable.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	body := p.buildRequestBody(req)
	if err := body.Validate(); err != nil {
		return nil, err
	}
	httpReq, err := p.buildHTTPRequest(ctx, body, true, req.Auth)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpDoer.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return nil, fmt.Errorf("anthropic: stream http %d: %s", resp.StatusCode, buf.String())
	}
	ch, _ := ParseStream(ctx, resp.Body)
	return ch, nil
}

// CountTokens implements core.Provider via a chars/4 + 1 per-message
// heuristic. The SDK does not expose a direct count endpoint, so callers
// needing exact counts should batch a count_tokens API when available.
func (p *Provider) CountTokens(_ context.Context, msgs []core.Message) (int, error) {
	n := 0
	for _, m := range msgs {
		for _, c := range m.Parts {
			if c.Kind == core.PART_KIND_PLAIN_TEXT {
				n += len(c.Text)/4 + 1
			}
		}
	}
	return n, nil
}

// buildRequestBody assembles a wire-format body from a core request and
// the provider's configured model. The model field is filled here so
// callers don't have to thread it through every call.
func (p *Provider) buildRequestBody(req core.ModelRequest) RequestBody {
	out := RequestBody{
		Model:     string(p.model),
		MaxTokens: maxTokensOrDefault(req),
		Messages:  toMessageParams(req.Messages),
	}
	if len(req.Tools) > 0 {
		out.Tools = toToolParams(req.Tools)
	}
	return out
}

// buildHTTPRequest marshals the wire body and stamps auth headers on the
// outbound request. Used by Stream; Generate goes through the SDK.
//
// override is the per-call auth (req.Auth). When empty we use the auth
// bound at construction time.
func (p *Provider) buildHTTPRequest(ctx context.Context, body RequestBody, stream bool, override core.Auth) (*http.Request, error) {
	body.Stream = stream
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", p.apiVer)
	p.applyAuthHeaders(req, override)
	return req, nil
}

// authOptions translates the per-call Auth override into SDK options.
// Empty override → no options; the SDK uses the credential bound at
// construction time.
func (p *Provider) authOptions(req core.ModelRequest) []option.RequestOption {
	a := req.Auth
	if a.APIKey == "" && a.Bearer == "" {
		return nil
	}
	var opts []option.RequestOption
	if a.Bearer != "" {
		opts = append(opts, option.WithAuthToken(a.Bearer))
	} else {
		opts = append(opts, option.WithAPIKey(a.APIKey))
	}
	if a.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(a.BaseURL))
	}
	return opts
}

// applyAuthHeaders stamps x-api-key OR Bearer on the outbound request
// plus any provider-specific overrides (e.g. anthropic-beta for OAuth).
// override takes precedence over p.auth when set.
func (p *Provider) applyAuthHeaders(req *http.Request, override core.Auth) {
	useOverride := override.APIKey != "" || override.Bearer != "" || len(override.Headers) > 0
	src := p.auth
	if useOverride {
		src = mergeAuth(p.auth, override)
	}
	if src.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+src.Bearer)
	} else if src.APIKey != "" {
		req.Header.Set("x-api-key", src.APIKey)
	}
	for k, v := range src.Headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
}

// mergeAuth returns base with override fields filled in where override
// has a non-zero value. We never mutate base or override.
func mergeAuth(base, override core.Auth) core.Auth {
	out := base
	if override.APIKey != "" {
		out.APIKey = override.APIKey
	}
	if override.Bearer != "" {
		out.Bearer = override.Bearer
	}
	if override.BaseURL != "" {
		out.BaseURL = override.BaseURL
	}
	if len(override.Headers) > 0 {
		merged := make(map[string]string, len(out.Headers)+len(override.Headers))
		for k, v := range out.Headers {
			merged[k] = v
		}
		for k, v := range override.Headers {
			merged[k] = v
		}
		out.Headers = merged
	}
	return out
}

// resolveEndpoint computes the /v1/messages URL for the configured
// base. Empty base falls back to the public Anthropic endpoint.
func resolveEndpoint(base string) string {
	if base == "" {
		return "https://api.anthropic.com/v1/messages"
	}
	return base + "/v1/messages"
}

// Compile-time: ensure Provider satisfies core.Provider.
var _ core.Provider = (*Provider)(nil)
