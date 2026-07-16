// Package responses defines typed OpenAI Responses wire DTOs.
package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Content is one Responses input or output content part.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
	FileData string `json:"file_data,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// ContentList accepts a string shorthand or typed content array.
type ContentList []Content

// UnmarshalJSON implements the Responses content union.
func (c *ContentList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("responses content: empty JSON")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return fmt.Errorf("responses content string: %w", err)
		}
		*c = ContentList{{Type: "input_text", Text: text}}
		return nil
	}
	if trimmed[0] != '[' {
		return fmt.Errorf("responses content: expected string or array")
	}
	var parts []Content
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return fmt.Errorf("responses content parts: %w", err)
	}
	*c = parts
	return nil
}

// InputItem is one Responses request input item.
type InputItem struct {
	ID        string      `json:"id,omitempty"`
	Type      string      `json:"type,omitempty"`
	Role      string      `json:"role,omitempty"`
	Content   ContentList `json:"content,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Arguments string      `json:"arguments,omitempty"`
	Output    string      `json:"output,omitempty"`
}

// Tool defines one Responses tool.
type Tool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Reasoning configures Responses reasoning.
type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Request is an OpenAI Responses request.
type Request struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	Tools              []Tool          `json:"tools,omitempty"`
	ToolChoice         json.RawMessage `json:"tool_choice,omitempty"`
	Reasoning          *Reasoning      `json:"reasoning,omitempty"`
	ParallelToolCalls  *bool           `json:"parallel_tool_calls,omitempty"`
}

// OutputItem is one Responses output item.
type OutputItem struct {
	ID        string      `json:"id,omitempty"`
	Type      string      `json:"type"`
	Role      string      `json:"role,omitempty"`
	Status    string      `json:"status,omitempty"`
	Content   ContentList `json:"content,omitempty"`
	Summary   ContentList `json:"summary,omitempty"`
	CallID    string      `json:"call_id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Arguments string      `json:"arguments,omitempty"`
}

// InputTokensDetails contains Responses cached-token usage.
type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// Usage is Responses token usage.
type Usage struct {
	InputTokens        int                 `json:"input_tokens"`
	OutputTokens       int                 `json:"output_tokens"`
	TotalTokens        int                 `json:"total_tokens,omitempty"`
	InputTokensDetails *InputTokensDetails `json:"input_tokens_details,omitempty"`
}

// ResponseError is a failed Responses result.
type ResponseError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// Response is a successful or failed OpenAI Responses result.
type Response struct {
	ID     string         `json:"id,omitempty"`
	Object string         `json:"object,omitempty"`
	Model  string         `json:"model,omitempty"`
	Output []OutputItem   `json:"output,omitempty"`
	Status string         `json:"status,omitempty"`
	Usage  *Usage         `json:"usage,omitempty"`
	Error  *ResponseError `json:"error,omitempty"`
}

// DecodeInput normalizes the accepted string-or-array input union.
func DecodeInput(raw json.RawMessage) ([]InputItem, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("decode responses input: input is required")
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("decode responses input string: %w", err)
		}
		return []InputItem{{Type: "message", Role: "user", Content: ContentList{{Type: "input_text", Text: text}}}}, nil
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("decode responses input: expected string or array")
	}
	var items []InputItem
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, fmt.Errorf("decode responses input items: %w", err)
	}
	for index := range items {
		if items[index].Type == "" && items[index].Role != "" {
			items[index].Type = "message"
		}
	}
	return items, nil
}

// DecodeRequest decodes and validates a Responses request.
func DecodeRequest(raw []byte) (*Request, error) {
	var request Request
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode responses request: %w", err)
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("decode responses request: model must not be blank")
	}
	if _, err := DecodeInput(request.Input); err != nil {
		return nil, err
	}
	return &request, nil
}

// DecodeResponse decodes a Responses result.
func DecodeResponse(raw []byte) (*Response, error) {
	var response Response
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode responses response: %w", err)
	}
	return &response, nil
}

// Encode serializes a Responses wire value.
func Encode(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode responses value: %w", err)
	}
	return body, nil
}
