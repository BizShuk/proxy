package minimax

import "encoding/json"

// Wire-format types for the Anthropic-Messages-compatible API exposed by
// minimax at https://api.minimax.io/anthropic/v1/messages. The shape is
// identical to Anthropic's /v1/messages endpoint, so we keep the same
// field names and JSON tags Anthropic uses:
//
//   - `system` is a TOP-LEVEL field, not a message with role=system.
//   - `messages` content is always an array of blocks (never a bare string).
//   - `tool_use` blocks carry their arguments as a JSON object under `input`.
//
// We do NOT use anthropic-sdk-go here because minimax is a thin
// Anthropic-compat surface — a stdlib HTTP wrapper is enough and avoids
// pulling in a second SDK dependency for one provider.

// RequestBody is the POST /v1/messages wire shape we send.
type RequestBody struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	Messages  []MessageParam   `json:"messages"`
	Tools     []ToolUnionParam `json:"tools,omitempty"`
	System    string           `json:"system,omitempty"`
	Stream    bool             `json:"stream,omitempty"`
}

// Response is the POST /v1/messages non-stream response shape.
type Response struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Content    []ContentBlock `json:"content"`
	Usage      Usage          `json:"usage"`
}

// ContentBlock is one element of response.content (text / tool_use / thinking).
type ContentBlock struct {
	Type  string          `json:"type"` // "text" | "tool_use" | "thinking"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use id
	Name  string          `json:"name,omitempty"`  // tool_use name
	Input json.RawMessage `json:"input,omitempty"` // tool_use args
}

// MessageParam is one element of request.messages.
type MessageParam struct {
	Role    string         `json:"role"` // "user" | "assistant"
	Content []ContentParam `json:"content"`
}

// ContentParam is one block of a message (text / tool_use / tool_result).
type ContentParam struct {
	Type      string          `json:"type"` // "text" | "tool_use" | "tool_result"
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ToolUnionParam is a single tool entry in request.tools. We keep it as
// a flat object (no OfTool wrapper) since minimax does not use the SDK's
// tagged-union encoding.
type ToolUnionParam struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Usage is the token accounting minimax returns.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
