package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
)

// OPENAI_CODEX_OAUTH_PROFILE_ID matches the profile.ID for the
// ChatGPT Plus/Pro OAuth path. The literal is owned by
// svc/upstream/profile.go (where the profile is registered); we
// re-declare it here so the handler can decide when to emit the
// structured codex payload log without importing the upstream
// package for one constant.
const OPENAI_CODEX_OAUTH_PROFILE_ID = "openai-codex-oauth"

// codexRequestPayloadSummary is the redacted view of a
// /codex/responses request body that the handler emits to slog.
// The struct intentionally exposes ONLY operational metadata —
// no instruction text, no user/assistant message content, no tool
// parameters. Free-text fields stay out of the summary so that
// even Debug-level emission cannot leak PII or system prompts.
type codexRequestPayloadSummary struct {
	Model             string   `json:"model"`
	Stream            bool     `json:"stream"`
	Store             bool     `json:"store"`
	ParallelToolCalls *bool    `json:"parallel_tool_calls,omitempty"`
	InstructionsBytes int      `json:"instructions_bytes"`
	HasInstructions   bool     `json:"has_instructions"`
	InputItems        int      `json:"input_items"`
	InputRoles        []string `json:"input_roles,omitempty"`
	ToolCount         int      `json:"tool_count"`
	ToolNames         []string `json:"tool_names,omitempty"`
	ParseError        string   `json:"parse_error,omitempty"`
}

// summarizeCodexRequestPayload parses a /codex/responses body and
// returns its operational metadata. The parser is deliberately
// tolerant: an unparseable body still produces a summary (with
// ParseError set) so the log can record the fact that a payload
// went out without crashing the request path.
//
// We never copy the value of `instructions`, `input[].content`,
// or `tools[].parameters` into the result — those fields are the
// common PII / system-prompt carriers and must stay out of logs.
func summarizeCodexRequestPayload(body []byte) codexRequestPayloadSummary {
	var raw struct {
		Model             string          `json:"model"`
		Stream            bool            `json:"stream"`
		Store             bool            `json:"store"`
		Instructions      string          `json:"instructions"`
		ParallelToolCalls *bool           `json:"parallel_tool_calls"`
		Input             []codexInputRef `json:"input"`
		Tools             []codexToolRef  `json:"tools"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return codexRequestPayloadSummary{ParseError: err.Error()}
	}
	s := codexRequestPayloadSummary{
		Model:             raw.Model,
		Stream:            raw.Stream,
		Store:             raw.Store,
		ParallelToolCalls: raw.ParallelToolCalls,
		InstructionsBytes: len(raw.Instructions),
		HasInstructions:   raw.Instructions != "",
		InputItems:        len(raw.Input),
		ToolCount:         len(raw.Tools),
	}
	for _, in := range raw.Input {
		role := in.Role
		if role == "" {
			// Codex input items can be "message", "function_call",
			// etc. when role is absent — capture the type label as a
			// fallback so the log still tells the operator what
			// shape was sent.
			role = in.Type
		}
		if role != "" {
			s.InputRoles = append(s.InputRoles, role)
		}
	}
	for _, t := range raw.Tools {
		if t.Name != "" {
			s.ToolNames = append(s.ToolNames, t.Name)
		}
	}
	return s
}

// codexInputRef is the minimal input-item shape used by the
// summary parser. It deliberately drops `content` (json.RawMessage
// only) so the summary cannot accidentally surface free-text.
type codexInputRef struct {
	Role string `json:"role"`
	Type string `json:"type"`
}

// codexToolRef drops `parameters` (JSON Schema) for the same
// reason — schema can carry sensitive field names.
type codexToolRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// logCodexRequestPayload emits a Debug-level structured log of
// the redacted codex request body. Callers gate on
// profile.ID == OPENAI_CODEX_OAUTH_PROFILE_ID before invoking.
//
// The summary fields are intentionally operational — see
// codexRequestPayloadSummary for the redacted shape.
func (h *Handler) logCodexRequestPayload(ctx context.Context, requestIDValue, routedModel string, body []byte, stream bool) {
	if h == nil {
		return
	}
	summary := summarizeCodexRequestPayload(body)
	if summary.ParseError != "" {
		slog.LogAttrs(ctx, slog.LevelDebug, "proxy codex request payload (unparseable)",
			slog.String("request_id", requestIDValue),
			slog.String("model", routedModel),
			slog.Bool("stream", stream),
			slog.Int("body_bytes", len(body)),
			slog.String("parse_error", summary.ParseError),
		)
		return
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestIDValue),
		slog.String("model", summary.Model),
		slog.Bool("stream", summary.Stream),
		slog.Bool("store", summary.Store),
		slog.Int("instructions_bytes", summary.InstructionsBytes),
		slog.Bool("has_instructions", summary.HasInstructions),
		slog.Int("input_items", summary.InputItems),
		slog.Any("input_roles", summary.InputRoles),
		slog.Int("tool_count", summary.ToolCount),
		slog.Any("tool_names", summary.ToolNames),
		slog.Int("body_bytes", len(body)),
	}
	// Emit *bool verbatim so the operator can distinguish
	// "parallel_tool_calls not set" (nil) from "explicitly false".
	if summary.ParallelToolCalls != nil {
		attrs = append(attrs, slog.Bool("parallel_tool_calls", *summary.ParallelToolCalls))
	} else {
		attrs = append(attrs, slog.String("parallel_tool_calls", "unset"))
	}
	slog.LogAttrs(ctx, slog.LevelDebug, "proxy codex request payload", attrs...)
}
