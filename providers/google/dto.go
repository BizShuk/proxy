// Package google provides a stdlib-only HTTP adapter for Google
// Generative AI's OpenAI-compatible /chat/completions endpoint. The
// default base URL points at
// https://generativelanguage.googleapis.com/v1beta/openai — Google
// publishes a wire-compatible surface that mirrors OpenAI's chat
// format.
//
// This file owns the wire-format DTOs shared by generate and stream.
// Shapes mirror OpenAI's /v1/chat/completions exactly.
package google

import "encoding/json"

// RequestBody is the POST /chat/completions wire shape we send upstream.
type RequestBody struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	Tools       []ToolDef     `json:"tools,omitempty"`
}

// ChatMessage is one entry in the messages array. Roles allowed by the
// OpenAI chat-completions spec: system | user | assistant | tool.
type ChatMessage struct {
	Role       string     `json:"role"`                   // system | user | assistant | tool
	Content    string     `json:"content,omitempty"`      // text content
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // for assistant messages with tool calls
	ToolCallID string     `json:"tool_call_id,omitempty"` // for tool messages
	Name       string     `json:"name,omitempty"`
}

// ToolCall is one assistant-side function invocation. Function.Arguments
// is a JSON-encoded string (NOT a nested object) — the spec requires it
// as a string for compatibility with most OpenAI-compatible servers.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // always "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolDef is the function descriptor published in the request `tools` array.
// Parameters should be the JSON Schema object the model should match.
type ToolDef struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// Response is the non-stream response shape from /chat/completions.
type Response struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// StreamChunk is one SSE event from /chat/completions?stream=true.
// FinishReason is a pointer so we can distinguish "not set" from "".
// Usage is only present on the terminal chunk (when the server reports it).
type StreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role      string     `json:"role,omitempty"`
			Content   string     `json:"content,omitempty"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}