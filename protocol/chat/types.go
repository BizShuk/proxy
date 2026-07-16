// Package chat defines typed OpenAI Chat Completions wire DTOs.
package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ImageURL is one Chat image URL part.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// ContentPart is one Chat multipart message content item.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// MessageContent is the Chat string-or-multipart content union.
type MessageContent struct {
	Text  string
	Parts []ContentPart
}

// UnmarshalJSON implements the Chat content union.
func (c *MessageContent) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("chat content: empty JSON")
	}
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &c.Text); err != nil {
			return fmt.Errorf("chat content string: %w", err)
		}
		return nil
	}
	if trimmed[0] != '[' {
		return fmt.Errorf("chat content: expected string, null, or array")
	}
	if err := json.Unmarshal(trimmed, &c.Parts); err != nil {
		return fmt.Errorf("chat content parts: %w", err)
	}
	return nil
}

// MarshalJSON preserves the selected Chat content shape.
func (c MessageContent) MarshalJSON() ([]byte, error) {
	if c.Parts != nil {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

// FunctionCall is a Chat tool call function payload.
type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// ToolCall is one complete Chat tool call.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// StreamToolCall is one indexed partial Chat tool call.
type StreamToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

// Message is one Chat conversation message.
type Message struct {
	Role             string          `json:"role"`
	Content          *MessageContent `json:"content,omitempty"`
	ToolCalls        []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
	Name             string          `json:"name,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

// FunctionDefinition defines a Chat function tool.
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Tool defines one Chat tool.
type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// Request is an OpenAI Chat Completions request.
type Request struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Stream              bool            `json:"stream,omitempty"`
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"`
	Tools               []Tool          `json:"tools,omitempty"`
	ToolChoice          json.RawMessage `json:"tool_choice,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	ParallelToolCalls   *bool           `json:"parallel_tool_calls,omitempty"`
}

// UsageDetails contains Chat prompt token details.
type UsageDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// Usage is Chat token usage.
type Usage struct {
	PromptTokens        int           `json:"prompt_tokens"`
	CompletionTokens    int           `json:"completion_tokens"`
	TotalTokens         int           `json:"total_tokens"`
	PromptTokensDetails *UsageDetails `json:"prompt_tokens_details,omitempty"`
}

// Choice is one complete Chat response choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Response is a successful Chat response.
type Response struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// StreamDelta is one Chat streaming delta.
type StreamDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolCalls        []StreamToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

// StreamChoice is one Chat stream choice.
type StreamChoice struct {
	Index        int         `json:"index"`
	Delta        StreamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// StreamChunk is one Chat SSE data payload.
type StreamChunk struct {
	ID      string         `json:"id,omitempty"`
	Object  string         `json:"object,omitempty"`
	Created int64          `json:"created,omitempty"`
	Model   string         `json:"model,omitempty"`
	Choices []StreamChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

// DecodeRequest decodes and validates a Chat request.
func DecodeRequest(raw []byte) (*Request, error) {
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode chat request: %w", err)
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("decode chat request: model must not be blank")
	}
	for index, message := range request.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return nil, fmt.Errorf("decode chat request: messages[%d].role must not be blank", index)
		}
	}
	return &request, nil
}

// DecodeResponse decodes a successful Chat response.
func DecodeResponse(raw []byte) (*Response, error) {
	var response Response
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	return &response, nil
}

// Encode serializes a Chat wire value.
func Encode(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode chat value: %w", err)
	}
	return body, nil
}

// TextContent creates a string Chat message content value.
func TextContent(text string) *MessageContent {
	return &MessageContent{Text: text}
}

// PartsContent creates a multipart Chat message content value.
func PartsContent(parts []ContentPart) *MessageContent {
	return &MessageContent{Parts: parts}
}
