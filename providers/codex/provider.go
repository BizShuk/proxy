package codex

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

// Provider implements core.Provider against the OpenAI Codex
// endpoint (chatgpt.com/backend-api/codex/responses). It is the
// sole entry point for clients — call New for the API-key path or
// NewWithOAuth for the ChatGPT-Plus/Pro OAuth path.
type Provider struct {
	baseURL   string
	apiKey    string
	bearer    string // OAuth access token; takes precedence over apiKey when set
	model     string
	accountID string
	client    *http.Client
}

// New returns a Provider using an API key (or OPENAI_API_KEY env
// fallback). For ChatGPT Plus/Pro OAuth use NewWithOAuth instead.
//
// model defaults to "gpt-5".
func New(opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	key := ResolveAPIKey(cfg.apiKey)
	if key == "" {
		return nil, fmt.Errorf("codex: API key not set (use WithAPIKey or OPENAI_API_KEY)")
	}
	return newProvider(cfg.baseURL, key, "", cfg.model, cfg.accountID), nil
}

// NewWithOAuth constructs a Provider from an OAuth credential
// produced by ExchangeCode / RefreshToken. The AccessToken is sent
// as Authorization: Bearer; AccountID becomes ChatGPT-Account-ID.
func NewWithOAuth(creds OAuthCredentials, opts ...Option) (*Provider, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("codex: OAuth access token is empty")
	}
	return newProvider(cfg.baseURL, "", creds.AccessToken, cfg.model, creds.AccountID), nil
}

func newProvider(baseURL, apiKey, bearer, model, accountID string) *Provider {
	return &Provider{
		baseURL:   strings.TrimRight(ResolveBaseURL(baseURL), "/"),
		apiKey:    apiKey,
		bearer:    bearer,
		model:     model,
		accountID: accountID,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

// ID implements core.Provider. Returns the family alone — "codex".
func (p *Provider) ID() string { return "codex" }

// Name is a convenience accessor returning "codex:<model>".
func (p *Provider) Name() string { return "codex:" + p.model }

// Models implements core.Provider. Returns the static catalog.
func (p *Provider) Models() []core.ModelSpec { return DefaultCatalog() }

// AuthSchemes implements core.Provider. Codex supports both the
// placeholder API-key path (mostly tests) and the production OAuth
// flow (NewWithOAuth).
func (p *Provider) AuthSchemes() []string {
	return []string{"api_key", "oauth"}
}

// Generate implements core.Provider. It POSTs the Codex-shaped
// request body and returns the folded ModelResult.
//
// The underlying /codex/responses endpoint always streams (we set
// stream: true unconditionally); a JSON-shaped response is returned
// only when stream=false, which is outside Codex's contract. The
// non-stream callers that anchor on this method go through a
// single-shot Generator that reads the entire SSE flow and folds
// it into one ModelResult — see Generate's implementation.
func (p *Provider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return core.ModelResult{}, err
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(raw))
	if err != nil {
		return core.ModelResult{}, err
	}
	p.applyHeaders(httpReq)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return core.ModelResult{}, fmt.Errorf("codex: http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		errBody, _ := io.ReadAll(resp.Body)
		return core.ModelResult{}, fmt.Errorf("codex: status %d: %s", resp.StatusCode, errBody)
	}
	// Codex always streams. Fold the SSE stream into a ModelResult.
	ch, err := ParseStream(ctx, resp.Body)
	if err != nil {
		return core.ModelResult{}, err
	}
	out := core.ModelResult{}
	for chunk := range ch {
		if chunk.Done {
			break
		}
		switch chunk.Kind {
		case core.PART_KIND_PLAIN_TEXT:
			out.Text += chunk.Text
			out.StopReason = "stop"
		case core.PART_KIND_TOOL_USE:
			if chunk.ToolUse != nil {
				out.ToolCalls = append(out.ToolCalls, core.ToolCall{
					ID:   chunk.ToolUse.ID,
					Name: chunk.ToolUse.Name,
					Args: chunk.ToolUse.Args,
				})
				out.StopReason = "tool_use"
			}
		}
	}
	return out, nil
}

// Stream implements core.Provider. It returns a channel of
// core.ModelChunk that the runtime consumes incrementally.
//
// The HTTP body stays open for the entire stream; callers should
// drain the channel before returning from their handler.
func (p *Provider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(), bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	p.applyHeaders(httpReq)
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("codex: http: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("codex: status %d: %s", resp.StatusCode, errBody)
	}
	return ParseStream(ctx, resp.Body)
}

// CountTokens implements core.Provider via a chars/4 + 1 per-message
// heuristic. Codex does not expose a direct count endpoint; callers
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

// ---------------------------------------------------------------------------
// internal — body construction
// ---------------------------------------------------------------------------

// buildRequestBody applies the Codex-specific transformations to a
// core.ModelRequest, producing the final RequestBody shape. The
// transformations are:
//
//   - lift system/developer messages out of input[] into the
//     top-level Instructions field
//   - force Stream=true, Store=false
//   - STRIP max_output_tokens (Codex rejects it; we intentionally
//     drop req.MaxTokens — see buildRequestBody)
//   - for lite models (IsLiteModel), force parallel_tool_calls=false
//
// The function never returns an error today; it returns one for the
// future case where a stricter Codex requirement surfaces.
func (p *Provider) buildRequestBody(req core.ModelRequest) (RequestBody, error) {
	body := RequestBody{
		Model:  p.model,
		Stream: true,
		Store:  false,
	}
	instructions, input := liftInstructions(req.Messages)
	body.Instructions = instructions
	body.Input = input
	body.Tools = translateTools(req.Tools)
	if IsLiteModel(p.model) {
		v := false
		body.ParallelToolCalls = &v
	}
	// intentionally NOT forwarding req.MaxTokens — Codex rejects
	// max_output_tokens on the wire, so the field is silently
	// dropped here.
	return body, nil
}

// liftInstructions extracts system/developer messages from
// msg.Parts and concatenates them into a single Instructions
// string. Non-instruction messages stay in the input list.
//
// Each text part is joined with "\n\n" so multi-part system
// prompts render as separate paragraphs in Codex's view.
func liftInstructions(msgs []core.Message) (string, []InputItem) {
	var instructions []string
	input := make([]InputItem, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == core.ROLE_SYSTEM {
			for _, c := range m.Parts {
				if c.Kind == core.PART_KIND_PLAIN_TEXT && c.Text != "" {
					instructions = append(instructions, c.Text)
				}
			}
			continue
		}
		role := "user"
		switch m.Role {
		case core.ROLE_ASSISTANT:
			role = "assistant"
		case core.ROLE_TOOL:
			role = "tool"
		}
		item := InputItem{Type: "message", Role: role}
		for _, c := range m.Parts {
			switch c.Kind {
			case core.PART_KIND_PLAIN_TEXT:
				if c.Text != "" {
					item.Content = append(item.Content, ContentBlock{Type: "input_text", Text: c.Text})
				}
			case core.PART_KIND_IMAGE:
				if len(c.Image) > 0 {
					mime := c.ImageMIME
					if mime == "" {
						mime = "image/png"
					}
					item.Content = append(item.Content, ContentBlock{
						Type: "input_image",
						ImageURL: &ImageURL{
							URL: fmt.Sprintf("data:%s;base64,%s", mime, string(c.Image)),
						},
					})
				}
			case core.PART_KIND_TOOL_USE:
				if c.ToolUse != nil {
					input = append(input, InputItem{
						Type: "message",
						Role: "assistant",
						Content: []ContentBlock{{
							Type: "input_text",
							Text: fmt.Sprintf("[assistant invoked tool %s with id=%s]", c.ToolUse.Name, c.ToolUse.ID),
						}},
					})
				}
			case core.PART_KIND_TOOL_RESULT:
				if c.ToolResult != nil {
					payload, _ := json.Marshal(c.ToolResult.Output)
					item.Content = append(item.Content, ContentBlock{
						Type: "input_text",
						Text: fmt.Sprintf("[tool %s returned ok=%v err=%q %s]", c.ToolResult.Name, c.ToolResult.OK, c.ToolResult.Error, string(payload)),
					})
				}
			}
		}
		input = append(input, item)
	}
	return strings.Join(instructions, "\n\n"), input
}

// translateTools converts core.ToolSpec → codex.Tool, copying the
// JSON Schema parameters verbatim. We accept either a json.RawMessage
// (most common — emitted by the schema generator) or any other type
// (we re-marshal it back to JSON so the wire shape is always raw).
func translateTools(specs []core.ToolSpec) []Tool {
	if len(specs) == 0 {
		return nil
	}
	out := make([]Tool, 0, len(specs))
	for _, s := range specs {
		t := Tool{
			Type:        "function",
			Name:        s.Name,
			Description: s.Description,
		}
		if raw, ok := s.Parameters.(json.RawMessage); ok {
			t.Parameters = raw
		} else if s.Parameters != nil {
			raw, _ := json.Marshal(s.Parameters)
			t.Parameters = raw
		}
		out = append(out, t)
	}
	return out
}

// fromResponse folds a non-stream Response into a core.ModelResult.
// Generate uses the streaming code path, so this helper is kept as a
// convenience for callers that want to feed a pre-marshalled Response
// in tests; it is not on the hot path.
func fromResponse(r Response) core.ModelResult {
	out := core.ModelResult{StopReason: r.StopReason}
	if r.Usage != nil {
		out.Usage = core.TokenUsage{
			PromptTokens:     r.Usage.InputTokens,
			CompletionTokens: r.Usage.OutputTokens,
			TotalTokens:      r.Usage.TotalTokens,
		}
	}
	for _, item := range r.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					out.Text += c.Text
				}
			}
		case "tool_call":
			out.ToolCalls = append(out.ToolCalls, core.ToolCall{
				ID:   item.ID,
				Name: item.Name,
				Args: decodeArgs(item.Arguments),
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// internal — HTTP transport
// ---------------------------------------------------------------------------

// endpoint is the upstream path appended to baseURL.
func (p *Provider) endpoint() string {
	return p.baseURL + "/codex/responses"
}

// applyHeaders sets the Codex identity headers plus auth. The set
// is fixed — Codex is picky about the header list:
//
//   - Content-Type
//   - originator: codex_cli_rs
//   - version:    0.125.0
//   - User-Agent: codex_cli_rs/0.125.0 (<platform>; <arch>)
//   - ChatGPT-Account-ID (when set)
//   - Authorization: Bearer <token>  (oauth or api key path)
func (p *Provider) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("originator", CodexOriginator)
	req.Header.Set("version", CodexVersion)
	req.Header.Set("User-Agent", CodexUserAgent())
	if p.accountID != "" {
		req.Header.Set("ChatGPT-Account-ID", p.accountID)
	}
	if p.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+p.bearer)
	} else if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// Compile-time: ensure *Provider satisfies core.Provider.
var _ core.Provider = (*Provider)(nil)
