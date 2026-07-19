// Package antigravity adapts Google's Antigravity OAuth-backed gateway
// to agentsdk's core.Provider interface.
//
// TODO: confirm the wire format (Anthropic-Messages path) and the live
// gateway endpoint against https://help.router-for-me/configuration/provider/antigravity
// once packet captures exist. The current implementation mirrors the
// Anthropic /v1/messages shape; the gateway may require a different
// path or additional headers (e.g. an Antigravity-specific beta flag).
//
// File layout:
//
//	provider.go    — entry point, Provider struct, interface methods
//	options.go     — functional options for New / NewWithOAuth
//	dto.go         — wire-format types (RequestBody, ContentBlock, ...)
//	validate.go    — RequestBody.Validate()
//	auth_api.go    — ResolveAPIKey / ResolveBaseURL
//	auth_oauth.go  — Google OAuth PKCE flow + loopback callback
//	stream.go      — SSE parser → core.ModelChunk
//	models.go      — DefaultCatalog
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bizshuk/agentsdk/core"
)

// Provider implements core.Provider against the Antigravity gateway.
type Provider struct {
	baseURL string
	apiKey  string
	bearer  string // OAuth access token (takes precedence over apiKey)
	model   string
	client  *http.Client
}

// New returns a Provider authenticated with an API key (direct key path).
// Falls back to ANTIGRAVITY_API_KEY env. model defaults to "claude-sonnet-5".
func New(opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	key := ResolveAPIKey(cfg.apiKey)
	if key == "" {
		return nil, fmt.Errorf("antigravity: API key not set (use WithAPIKey or ANTIGRAVITY_API_KEY)")
	}
	return &Provider{
		baseURL: strings.TrimRight(ResolveBaseURL(cfg.baseURL), "/"),
		apiKey:  key,
		model:   cfg.model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// NewWithOAuth returns a Provider authenticated with an OAuth access token.
// The token is sent as Authorization: Bearer <token> on every request.
func NewWithOAuth(creds OAuthCredentials, opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("antigravity: OAuth access token is empty")
	}
	return &Provider{
		baseURL: strings.TrimRight(ResolveBaseURL(cfg.baseURL), "/"),
		bearer:  creds.AccessToken,
		model:   cfg.model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// ID implements core.Provider. Returns the family alone — "antigravity".
func (p *Provider) ID() string { return "antigravity" }

// Name is a convenience accessor returning "antigravity:<model>".
func (p *Provider) Name() string { return "antigravity:" + p.model }

// Models implements core.Provider. Returns the static catalog.
func (p *Provider) Models() []core.ModelSpec { return DefaultCatalog() }

// AuthSchemes implements core.Provider. Antigravity accepts long-lived API
// keys (some deployments) AND Google OAuth access tokens.
func (p *Provider) AuthSchemes() []string { return []string{"api_key", "oauth"} }

// Generate implements core.Provider — blocking POST to /v1/messages.
func (p *Provider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	body, err := buildRequestBody(req, p.model)
	if err != nil {
		return core.ModelResult{}, err
	}
	if err := body.Validate(); err != nil {
		return core.ModelResult{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("antigravity: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(raw))
	if err != nil {
		return core.ModelResult{}, err
	}
	p.applyHeaders(httpReq)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("antigravity: http: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return core.ModelResult{}, fmt.Errorf("antigravity: status %d: %s", resp.StatusCode, respBody)
	}
	var r Response
	if err := json.Unmarshal(respBody, &r); err != nil {
		return core.ModelResult{}, fmt.Errorf("antigravity: decode: %w", err)
	}
	return fromResponse(r), nil
}

// Stream implements core.Provider — SSE POST to /v1/messages.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	body, err := buildRequestBody(req, p.model)
	if err != nil {
		return nil, err
	}
	if err := body.Validate(); err != nil {
		return nil, err
	}
	body.Stream = true
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("antigravity: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")
	p.applyHeaders(httpReq)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("antigravity: http: %w", err)
	}
	// Caller is responsible for closing resp.Body; Stream is fire-and-forget
	// once ParseStream has the reader.
	ch, _ := ParseStream(ctx, resp.Body)
	return ch, nil
}

// CountTokens implements core.Provider via chars/4 + 1 per-message
// heuristic. Accurate counts should batch a real count_tokens API when
// the gateway exposes one; until then this gives a stable upper bound.
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

func (p *Provider) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	// Anthropic-Messages wire shape carries the API version as a header.
	// Antigravity mirrors Anthropic here — confirm against the gateway
	// docs if a version mismatch surfaces.
	req.Header.Set("anthropic-version", "2023-06-01")
	if p.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+p.bearer)
	} else if p.apiKey != "" {
		req.Header.Set("x-api-key", p.apiKey)
	}
}

// ---------------------------------------------------------------------------
// internal — body construction
// ---------------------------------------------------------------------------

func buildRequestBody(req core.ModelRequest, model string) (RequestBody, error) {
	out := RequestBody{
		Model:     model,
		MaxTokens: maxTokensOrDefault(req),
		Messages:  toMessageParams(req.Messages),
	}
	if len(req.Tools) > 0 {
		out.Tools = toToolParams(req.Tools)
	}
	// Collect any system-role messages into the top-level System field.
	// Anthropic-Messages expects `system` outside the messages array.
	var sys strings.Builder
	for _, m := range req.Messages {
		if m.Role != core.ROLE_SYSTEM {
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == core.PART_KIND_PLAIN_TEXT && p.Text != "" {
				if sys.Len() > 0 {
					sys.WriteString("\n\n")
				}
				sys.WriteString(p.Text)
			}
		}
	}
	if sys.Len() > 0 {
		out.System = sys.String()
	}
	return out, nil
}

func maxTokensOrDefault(req core.ModelRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return 4096
}

func toMessageParams(msgs []core.Message) []MessageParam {
	out := make([]MessageParam, 0, len(msgs))
	for _, m := range msgs {
		// system is hoisted to the top-level System field.
		if m.Role == core.ROLE_SYSTEM {
			continue
		}
		role := "user"
		if m.Role == core.ROLE_ASSISTANT {
			role = "assistant"
		}
		var blocks []ContentParam
		for _, c := range m.Parts {
			switch c.Kind {
			case core.PART_KIND_PLAIN_TEXT:
				if c.Text != "" {
					blocks = append(blocks, ContentParam{Type: "text", Text: c.Text})
				}
			case core.PART_KIND_TOOL_USE:
				if c.ToolUse != nil {
					input, _ := json.Marshal(c.ToolUse.Args)
					blocks = append(blocks, ContentParam{
						Type:  "tool_use",
						ID:    c.ToolUse.ID,
						Name:  c.ToolUse.Name,
						Input: input,
					})
				}
			case core.PART_KIND_TOOL_RESULT:
				if c.ToolResult != nil {
					payload, _ := json.Marshal(c.ToolResult.Output)
					blocks = append(blocks, ContentParam{
						Type:      "tool_result",
						ToolUseID: c.ToolResult.CallID,
						Content:   payload,
						IsError:   c.ToolResult.Error != "",
					})
				}
			}
		}
		out = append(out, MessageParam{Role: role, Content: blocks})
	}
	return out
}

func toToolParams(specs []core.ToolSpec) []ToolUnionParam {
	out := make([]ToolUnionParam, 0, len(specs))
	for _, s := range specs {
		var schema json.RawMessage
		if raw, ok := s.Parameters.(json.RawMessage); ok && len(raw) > 0 {
			schema = raw
		} else if s.Parameters != nil {
			if m, err := json.Marshal(s.Parameters); err == nil {
				schema = m
			}
		}
		out = append(out, ToolUnionParam{
			Name:        s.Name,
			Description: s.Description,
			InputSchema: schema,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// internal — response folding
// ---------------------------------------------------------------------------

func fromResponse(r Response) core.ModelResult {
	out := core.ModelResult{
		StopReason: r.StopReason,
		Usage: core.TokenUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.InputTokens + r.Usage.OutputTokens,
		},
	}
	for _, block := range r.Content {
		switch block.Type {
		case "text":
			out.Text += block.Text
		case "tool_use":
			if block.ID != "" {
				var argsMap map[string]any
				_ = json.Unmarshal(block.Input, &argsMap)
				out.ToolCalls = append(out.ToolCalls, core.ToolCall{
					ID:   block.ID,
					Name: block.Name,
					Args: argsMap,
				})
			}
		}
	}
	return out
}

// Compile-time: ensure Provider satisfies core.Provider.
var _ core.Provider = (*Provider)(nil)