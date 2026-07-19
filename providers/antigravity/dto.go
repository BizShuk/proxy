package antigravity

// Wire format is Anthropic-Messages-compatible. Antigravity exposes claude-*
// models via the Anthropic-Messages endpoint and gemini-*/gpt-* models via
// either an OpenAI-compat shim or the same /v1/messages shape; we pick the
// Anthropic-Messages path because the user-facing model surface is mostly
// claude-* and gemini-* (both work over Anthropic-Messages).
//
// TODO: confirm base URL + endpoint path against
// https://help.router-for-me/configuration/provider/antigravity once the
// Antigravity gateway is fully wired and packet captures exist.

import "encoding/json"

// RequestBody is the POST /v1/messages wire shape we send. Mirrors the
// Anthropic Messages API: top-level `system`, `messages` is an array of
// role+content-blocks, `tools` is a flat array of tool definitions.
type RequestBody struct {
	Model     string           `json:"model"`
	MaxTokens int              `json:"max_tokens"`
	Messages  []MessageParam   `json:"messages"`
	System    string           `json:"system,omitempty"`
	Stream    bool             `json:"stream,omitempty"`
	Tools     []ToolUnionParam `json:"tools,omitempty"`
}

// MessageParam is one entry in request.messages.
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

// ToolUnionParam is the wire shape of one entry in request.tools. Antigravity
// accepts a flat {name, description, input_schema} object — no union wrapper
// like anthropic-sdk-go uses.
type ToolUnionParam struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
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

// Usage is the token accounting on the response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}