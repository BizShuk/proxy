// Package grok adapts xAI's Grok API (OpenAI-compatible chat-completions
// endpoint) to agentsdk's core.Provider interface.
//
// File layout:
//
//   - provider.go    — entry point, Provider struct, interface methods
//   - options.go     — functional options for New / NewWithOAuth
//   - dto.go         — wire-format types (RequestBody, Response, ...)
//   - validate.go    — RequestBody.Validate()
//   - auth_api.go    — ResolveAPIKey / ResolveBaseURL
//   - auth_oauth.go  — PKCE helpers, OAuthCredentials, OpenBrowser
//   - stream.go      — SSE parser -> core.ModelChunk
//   - models.go      — DefaultCatalog
//
// xAI supports two auth flavors. The API-key path goes through New(); the
// OAuth path (SuperGrok / X Premium subscription) goes through
// NewWithOAuth(). Both honor Bearer Authorization on the wire — the
// difference is whether the credential came from a long-lived key or a
// freshly exchanged access token.
package grok

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

// Provider implements core.Provider against the xAI Grok API.
type Provider struct {
	baseURL string
	apiKey  string // API-key path
	bearer  string // OAuth path; takes precedence over apiKey when set
	model   string
	client  *http.Client
}

// New returns a Provider using an API key (or XAI_API_KEY env fallback).
// model defaults to "grok-3".
func New(opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	key := ResolveAPIKey(cfg.apiKey)
	if key == "" {
		return nil, fmt.Errorf("grok: API key not set (use WithAPIKey or XAI_API_KEY)")
	}
	return &Provider{
		baseURL: strings.TrimRight(ResolveBaseURL(cfg.baseURL), "/"),
		apiKey:  key,
		model:   cfg.model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// NewWithOAuth constructs a provider from an OAuth credential. The OAuth
// bearer takes precedence over any baked-in apiKey for every request —
// callers do not need to pass both.
//
// If the supplied credentials are expired, NewWithOAuth still returns a
// usable provider; callers are expected to call RefreshToken separately
// before issuing requests.
func NewWithOAuth(creds OAuthCredentials, opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("grok: OAuth access token is empty")
	}
	return &Provider{
		baseURL: strings.TrimRight(ResolveBaseURL(cfg.baseURL), "/"),
		bearer:  creds.AccessToken,
		model:   cfg.model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

// ID implements core.Provider. Returns the family alone — "grok".
func (p *Provider) ID() string { return "grok" }

// Name is a convenience accessor returning "grok:<model>".
func (p *Provider) Name() string { return "grok:" + p.model }

// Models implements core.Provider. Returns the static catalog.
func (p *Provider) Models() []core.ModelSpec { return DefaultCatalog() }

// AuthSchemes implements core.Provider. Grok accepts long-lived API
// keys AND OAuth access tokens (SuperGrok / X Premium).
func (p *Provider) AuthSchemes() []string {
	return []string{"api_key", "oauth"}
}

// Generate implements core.Provider.
func (p *Provider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	body := RequestBody{
		Model:     p.model,
		MaxTokens: maxTokensOrDefault(req),
		Messages:  toChatMessages(req.Messages),
		Stream:    false,
	}
	if len(req.Tools) > 0 {
		body.Tools = toToolDefs(req.Tools)
	}
	if err := body.Validate(); err != nil {
		return core.ModelResult{}, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("grok: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return core.ModelResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", p.authHeader())
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("grok: http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("grok: read: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return core.ModelResult{}, fmt.Errorf("grok: status %d: %s", resp.StatusCode, string(respBody))
	}
	var cr Response
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return core.ModelResult{}, fmt.Errorf("grok: decode: %w", err)
	}
	return fromResponse(cr), nil
}

// Stream implements core.Provider. Streams SSE chunks and forwards them
// as core.ModelChunk.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	body := RequestBody{
		Model:    p.model,
		Messages: toChatMessages(req.Messages),
		Stream:   true,
	}
	if len(req.Tools) > 0 {
		body.Tools = toToolDefs(req.Tools)
	}
	if err := body.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("grok: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", p.authHeader())
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("grok: http: %w", err)
	}
	return ParseStream(ctx, resp.Body), nil
}

// CountTokens implements core.Provider via a chars/4 + 1 per-message
// heuristic. xAI does not expose a direct count endpoint, so callers
// needing exact counts should fall back to the upstream response.
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

// authHeader returns the Authorization header value. OAuth bearer wins
// over a baked-in API key when both are present, matching the priority
// documented for the anthropic provider.
func (p *Provider) authHeader() string {
	if p.bearer != "" {
		return "Bearer " + p.bearer
	}
	return "Bearer " + p.apiKey
}

// ---------------------------------------------------------------------------
// internal — DTO translation
// ---------------------------------------------------------------------------

func toChatMessages(msgs []core.Message) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		role := "user"
		switch m.Role {
		case core.ROLE_SYSTEM:
			role = "system"
		case core.ROLE_ASSISTANT:
			role = "assistant"
		case core.ROLE_TOOL:
			role = "tool"
		}
		text, toolCalls, toolResults := flattenMessage(m)
		cm := ChatMessage{Role: role, Content: text, ToolCalls: toolCalls}
		if len(toolResults) > 0 {
			cm.ToolCallID = toolResults[0].CallID
			cm.Content = toolResults[0].OutputAsString()
			cm.Name = toolResults[0].Name
		}
		out = append(out, cm)
	}
	return out
}

type flatToolResult struct {
	CallID string
	Name   string
	Output any
}

func (r flatToolResult) OutputAsString() string {
	if s, ok := r.Output.(string); ok {
		return s
	}
	raw, _ := json.Marshal(r.Output)
	return string(raw)
}

func flattenMessage(m core.Message) (string, []ToolCall, []flatToolResult) {
	var sb strings.Builder
	var tcs []ToolCall
	var trs []flatToolResult
	for _, c := range m.Parts {
		switch c.Kind {
		case core.PART_KIND_PLAIN_TEXT:
			sb.WriteString(c.Text)
		case core.PART_KIND_TOOL_USE:
			if c.ToolUse != nil {
				args, _ := json.Marshal(c.ToolUse.Args)
				tcs = append(tcs, ToolCall{
					ID: c.ToolUse.ID, Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{Name: c.ToolUse.Name, Arguments: string(args)},
				})
			}
		case core.PART_KIND_TOOL_RESULT:
			if c.ToolResult != nil {
				trs = append(trs, flatToolResult{
					CallID: c.ToolResult.CallID,
					Name:   c.ToolResult.Name,
					Output: c.ToolResult.Output,
				})
			}
		}
	}
	return sb.String(), tcs, trs
}

func toToolDefs(schemas []core.ToolSpec) []ToolDef {
	out := make([]ToolDef, 0, len(schemas))
	for _, s := range schemas {
		td := ToolDef{Type: "function"}
		td.Function.Name = s.Name
		td.Function.Description = s.Description
		if raw, ok := s.Parameters.(json.RawMessage); ok {
			td.Function.Parameters = raw
		}
		out = append(out, td)
	}
	return out
}

func fromResponse(cr Response) core.ModelResult {
	out := core.ModelResult{
		StopReason: "",
		Usage: core.TokenUsage{
			PromptTokens:     cr.Usage.PromptTokens,
			CompletionTokens: cr.Usage.CompletionTokens,
			TotalTokens:      cr.Usage.TotalTokens,
		},
	}
	for _, c := range cr.Choices {
		out.Text += c.Message.Content
		out.StopReason = c.FinishReason
		for _, tc := range c.Message.ToolCalls {
			var args map[string]any
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			out.ToolCalls = append(out.ToolCalls, core.ToolCall{
				ID: tc.ID, Name: tc.Function.Name, Args: args,
			})
		}
	}
	return out
}

func maxTokensOrDefault(req core.ModelRequest) int {
	if req.MaxTokens > 0 {
		return req.MaxTokens
	}
	return 4096
}

// Compile-time: ensure Provider satisfies core.Provider.
var _ core.Provider = (*Provider)(nil)