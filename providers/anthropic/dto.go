package anthropic

import "encoding/json"

// This file re-exports the Anthropic /v1/messages wire-format types under
// our own namespace so callers outside this package do not need to import
// anthropic-sdk-go directly. Wire shapes are kept in sync with the public
// docs at https://docs.anthropic.com/en/api/messages.
//
// Note: Anthropic's Messages API differs from the chat-completion APIs in
// three structural ways:
//
//   - `system` is a TOP-LEVEL field, not a message with role=system.
//   - `messages` content is always an array of blocks (never a bare string).
//   - `tool_use` blocks carry their arguments as a JSON object under `input`.

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
// We keep the wire shape Anthropic expects.
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

// ToolUnionParam mirrors the SDK's union shape so request body JSON is
// identical to what anthropic-sdk-go emits.
type ToolUnionParam struct {
	OfTool *ToolParam `json:"-"`
}

// MarshalJSON emits the underlying tool param under the same shape the SDK
// produces: a single `{name, description, input_schema}` object.
func (t ToolUnionParam) MarshalJSON() ([]byte, error) {
	if t.OfTool == nil {
		return []byte("null"), nil
	}
	return json.Marshal(t.OfTool)
}

// ToolParam is the wire shape of one entry in request.tools.
type ToolParam struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	InputSchema ToolInputSchema   `json:"input_schema"`
}

// ToolInputSchema is the loose {"type":"object",...} envelope the SDK
// accepts; we pass the caller's parameters blob through verbatim.
type ToolInputSchema struct {
	Type       any    `json:"type,omitempty"` // always "object" in practice
	Properties any    `json:"-"`              // raw JSON object via custom marshal
	Extra      string `json:"-"`              // raw tail to forward
}

// MarshalJSON forwards the original JSON object verbatim when the caller
// supplied a json.RawMessage; otherwise emits a minimal `{"type":"object"}`.
func (s ToolInputSchema) MarshalJSON() ([]byte, error) {
	if raw, ok := s.Properties.(json.RawMessage); ok && len(raw) > 0 {
		return raw, nil
	}
	if s.Type == nil {
		return []byte(`{"type":"object"}`), nil
	}
	return json.Marshal(struct {
		Type any `json:"type"`
	}{Type: s.Type})
}

// Usage is Anthropic's token accounting.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
