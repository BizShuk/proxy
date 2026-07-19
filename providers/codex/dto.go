// Package codex adapts ChatGPT-Plus/Pro OAuth credentials to the
// agentsdk core.Provider interface.
//
// Wire format: OpenAI Responses with Codex-specific transformations:
//   - Strip max_output_tokens (Codex rejects it)
//   - Force stream: true, store: false
//   - Lift system/developer messages from input[] into top-level
//     instructions field
//   - For "lite" models (gpt-5.6, gpt-5.6-sol), force
//     parallel_tool_calls: false
//
// Plus identity headers that mark the request as coming from the
// Codex CLI:
//
//	originator: codex_cli_rs
//	version:    0.125.0
//	User-Agent: codex_cli_rs/0.125.0 (<platform>; <arch>)
//
// File layout:
//
//   - provider.go    — entry point, Provider struct, interface methods
//   - options.go     — functional options for New
//   - dto.go         — wire-format types (RequestBody, Response, StreamChunk)
//   - validate.go    — RequestBody.Validate()
//   - auth_api.go    — ResolveAPIKey / ResolveBaseURL
//   - auth_oauth.go  — OAuth PKCE flow + token refresh
//   - stream.go      — SSE parser → core.ModelChunk
//   - models.go      — DefaultCatalog and IsLiteModel
package codex

import "encoding/json"

// RequestBody is what we POST to
// https://chatgpt.com/backend-api/codex/responses. The package
// applies the Codex-specific mutations (lift instructions, force
// stream/store, strip max_output_tokens) before marshalling; what
// arrives here is already the FINAL wire shape.
type RequestBody struct {
	Model       string      `json:"model"`
	Instructions string     `json:"instructions"`                // lifted system+developer messages joined with "\n\n"
	Input       []InputItem `json:"input"`                       // non-instruction messages
	Stream      bool        `json:"stream"`                      // always true
	Store       bool        `json:"store"`                       // always false
	Tools       []Tool      `json:"tools,omitempty"`

	// The following two fields are intentionally NOT serialized by
	// Codex. MaxOutputTokens is stripped in provider.buildRequestBody
	// (Codex rejects it). We keep the field declaration here as
	// documentation only — callers should set it via req.MaxTokens,
	// which the provider drops.
	//
	// ParallelToolCalls is set only when the model is a "lite" model
	// (see IsLiteModel).
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`
}

// InputItem is one element of request.input.
type InputItem struct {
	Type    string         `json:"type"`     // "message"
	Role    string         `json:"role"`     // "user" | "assistant" | "tool"
	Content []ContentBlock `json:"content"`
}

// ContentBlock is one block of an InputItem.Content or an
// OutputItem.Content. The discriminator type distinguishes text
// from image inputs and from assistant outputs.
type ContentBlock struct {
	Type     string    `json:"type"`               // "input_text" | "output_text" | "input_image"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL holds a data: or http(s): URL for an image input.
type ImageURL struct {
	URL string `json:"url"`
}

// Tool is the wire shape of one entry in request.tools. Codex uses
// the same shape as the public OpenAI Responses API: a top-level
// `type: "function"` discriminator with name/description/parameters.
type Tool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Response is the non-stream shape returned by /codex/responses when
// stream=false. (The Codex /codex endpoint always streams when
// stream=true; this is here only for completeness.)
type Response struct {
	ID         string       `json:"id"`
	Object     string       `json:"object"`
	Model      string       `json:"model"`
	Output     []OutputItem `json:"output"`
	StopReason string       `json:"stop_reason,omitempty"` // "stop" | "length" | "tool_use"
	Usage      *Usage       `json:"usage,omitempty"`
}

// OutputItem is one element of response.output. The `type`
// discriminator distinguishes a message (text reply) from a tool call.
type OutputItem struct {
	Type    string         `json:"type"`               // "message" | "tool_call"
	Role    string         `json:"role,omitempty"`      // "assistant" when type=message
	Content []ContentBlock `json:"content,omitempty"`  // populated when type=message
	// tool_call fields:
	ID        string `json:"id,omitempty"`        // tool call id
	Name      string `json:"name,omitempty"`      // tool name
	Arguments string `json:"arguments,omitempty"` // raw JSON-encoded args
}

// Usage is Codex's token accounting — same shape as the public
// OpenAI Responses schema.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// StreamChunk is one SSE event delivered by /codex/responses?stream=true.
// Codex uses the same event vocabulary as the public Responses stream;
// we only care about the subset here.
type StreamChunk struct {
	Type     string       `json:"type"` // "response.output_text.delta" | "response.output_item.done" | "response.completed" | "error" | ...
	Item     *OutputItem  `json:"item,omitempty"`
	Delta    string       `json:"delta,omitempty"`
	Response *Response    `json:"response,omitempty"`
	Error    *StreamError `json:"error,omitempty"`
}

// StreamError is the error event payload. The SDK surfaces this as a
// terminal chunk to mirror how the runtime recognizes stream-end.
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
