// Package anthropic defines typed Anthropic Messages wire DTOs.
package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Thinking configures Anthropic extended thinking.
type Thinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// Source contains an inline Anthropic media source.
type Source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Content is one Anthropic message content block.
type Content struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      json.RawMessage `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	Source       *Source         `json:"source,omitempty"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// ContentList accepts either Anthropic's string shorthand or a block array.
type ContentList []Content

// UnmarshalJSON implements the string-or-block union.
func (c *ContentList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("anthropic content: empty JSON")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return fmt.Errorf("anthropic content string: %w", err)
		}
		*c = ContentList{{Type: "text", Text: text}}
		return nil
	}
	var blocks []Content
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return fmt.Errorf("anthropic content blocks: %w", err)
	}
	*c = blocks
	return nil
}

// Message is one Anthropic conversation message.
type Message struct {
	Role    string      `json:"role"`
	Content ContentList `json:"content"`
}

// Tool defines one Anthropic callable tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolChoice selects Anthropic tool behavior.
type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// UnmarshalJSON accepts the documented object and a common string shorthand.
func (t *ToolChoice) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		return json.Unmarshal(trimmed, &t.Type)
	}
	type alias ToolChoice
	if err := json.Unmarshal(trimmed, (*alias)(t)); err != nil {
		return fmt.Errorf("anthropic tool choice: %w", err)
	}
	return nil
}

// Request is an Anthropic Messages request.
type Request struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	System        ContentList     `json:"system,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
	Thinking      *Thinking       `json:"thinking,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

// Usage is Anthropic token usage.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// Response is a successful Anthropic message response.
type Response struct {
	ID           string      `json:"id"`
	Type         string      `json:"type"`
	Role         string      `json:"role"`
	Content      ContentList `json:"content"`
	Model        string      `json:"model"`
	StopReason   string      `json:"stop_reason"`
	StopSequence string      `json:"stop_sequence,omitempty"`
	Usage        Usage       `json:"usage"`
}

// DecodeRequest decodes and validates an Anthropic request.
func DecodeRequest(raw []byte) (*Request, error) {
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode anthropic request: %w", err)
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("decode anthropic request: model must not be blank")
	}
	if request.MaxTokens < 0 {
		return nil, fmt.Errorf("decode anthropic request: max_tokens must not be negative")
	}
	for index, message := range request.Messages {
		if message.Role == "" {
			return nil, fmt.Errorf("decode anthropic request: messages[%d].role must not be blank", index)
		}
		if message.Content == nil {
			return nil, fmt.Errorf("decode anthropic request: messages[%d].content is required", index)
		}
	}
	return &request, nil
}

// DecodeResponse decodes a successful Anthropic response.
func DecodeResponse(raw []byte) (*Response, error) {
	var response Response
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}
	return &response, nil
}

// Encode serializes an Anthropic wire value.
func Encode(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode anthropic value: %w", err)
	}
	return body, nil
}
