package adaptor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Anthropic DTOs
// ---------------------------------------------------------------------------

type AnthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type AnthropicRequest struct {
	Model       string                  `json:"model"`
	Messages    []AnthropicMessage      `json:"messages"`
	System      any                     `json:"system,omitempty"` // string or []AnthropicContent
	MaxTokens   int                     `json:"max_tokens,omitempty"`
	Stream      bool                    `json:"stream,omitempty"`
	Temperature *float64                `json:"temperature,omitempty"`
	TopP        *float64                `json:"top_p,omitempty"`
	Tools       []AnthropicTool         `json:"tools,omitempty"`
	ToolChoice  any                     `json:"tool_choice,omitempty"`
	Thinking    *AnthropicThinking      `json:"thinking,omitempty"`
}

type AnthropicMessage struct {
	Role    string             `json:"role"`
	Content []AnthropicContent `json:"content"`
}

func (m *AnthropicMessage) UnmarshalJSON(data []byte) error {
	type Alias AnthropicMessage
	var aux struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Role = aux.Role
	if len(aux.Content) == 0 {
		return nil
	}

	trimmed := aux.Content
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\n' || trimmed[0] == '\r') {
		trimmed = trimmed[1:]
	}
	if len(trimmed) == 0 {
		return nil
	}

	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(aux.Content, &str); err != nil {
			return err
		}
		m.Content = []AnthropicContent{{Type: "text", Text: str}}
	} else {
		var blocks []AnthropicContent
		if err := json.Unmarshal(aux.Content, &blocks); err != nil {
			return err
		}
		m.Content = blocks
	}
	return nil
}

type AnthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type AnthropicContent struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	Thinking  string           `json:"thinking,omitempty"`  // thinking block text
	ID        string           `json:"id,omitempty"`        // tool_use id
	Name      string           `json:"name,omitempty"`      // tool_use name
	Input     json.RawMessage  `json:"input,omitempty"`     // tool_use arguments JSON
	ToolUseID string           `json:"tool_use_id,omitempty"` // tool_result id
	Content   any              `json:"content,omitempty"`   // tool_result content (string or block list)
	IsError   bool             `json:"is_error,omitempty"`  // tool_result error flag
	Source    *AnthropicSource `json:"source,omitempty"`
}

type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type AnthropicResponse struct {
	ID           string             `json:"id"`
	Type         string             `json:"type"` // "message"
	Role         string             `json:"role"` // "assistant"
	Content      []AnthropicContent `json:"content"`
	Model        string             `json:"model"`
	StopReason   string             `json:"stop_reason"`
	StopSequence string             `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage     `json:"usage"`
}

type AnthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// OpenAI DTOs
// ---------------------------------------------------------------------------

type OpenAIChatRequest struct {
	Model           string          `json:"model"`
	Messages        []OpenAIMessage `json:"messages"`
	MaxTokens       int             `json:"max_tokens,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Tools           []OpenAITool    `json:"tools,omitempty"`
	ToolChoice      any             `json:"tool_choice,omitempty"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type OpenAIMessage struct {
	Role             string            `json:"role"`
	Content          any               `json:"content,omitempty"` // string or []OpenAIContentPart
	ToolCalls        []OpenAIToolCall  `json:"tool_calls,omitempty"`
	ToolCallID       string            `json:"tool_call_id,omitempty"`
	Name             string            `json:"name,omitempty"`
	ReasoningContent string            `json:"reasoning_content,omitempty"`
}

type OpenAIContentPart struct {
	Type     string         `json:"type"`
	Text     string         `json:"text,omitempty"`
	ImageURL *OpenAIImageURL `json:"image_url,omitempty"`
}

type OpenAIImageURL struct {
	URL string `json:"url"`
}

type OpenAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function OpenAIFunctionCall `json:"function"`
}

type OpenAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // stringified JSON
}

type OpenAITool struct {
	Type     string            `json:"type"` // "function"
	Function OpenAIFunctionDef `json:"function"`
}

type OpenAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type OpenAIChatResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIChatChoice   `json:"choices"`
	Usage   OpenAIChatUsage      `json:"usage"`
}

type OpenAIChatChoice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type OpenAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type OpenAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []OpenAIStreamChoice `json:"choices"`
}

type OpenAIStreamChoice struct {
	Index        int                 `json:"index"`
	Delta        OpenAIStreamDelta   `json:"delta"`
	FinishReason string              `json:"finish_reason,omitempty"`
}

type OpenAIStreamDelta struct {
	Role             string           `json:"role,omitempty"`
	Content          string           `json:"content,omitempty"`
	ToolCalls        []OpenAIToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

// ---------------------------------------------------------------------------
// Translations: Anthropic Messages ⇄ OpenAI Chat Completions
// ---------------------------------------------------------------------------

func translateToolChoice(src any) any {
	if src == nil {
		return nil
	}

	if str, ok := src.(string); ok {
		if str == "any" {
			return "required"
		}
		return str
	}

	if m, ok := src.(map[string]any); ok {
		t, _ := m["type"].(string)
		switch t {
		case "auto":
			return "auto"
		case "any":
			return "required"
		case "tool":
			name, _ := m["name"].(string)
			if name != "" {
				return map[string]any{
					"type": "function",
					"function": map[string]any{
						"name": name,
					},
				}
			}
		}
	}

	return src
}

// TranslateAnthropicToOpenAI maps an Anthropic Message payload to OpenAI Chat Completions payload.
func TranslateAnthropicToOpenAI(src *AnthropicRequest) *OpenAIChatRequest {
	dst := &OpenAIChatRequest{
		Model:       src.Model,
		MaxTokens:   src.MaxTokens,
		Stream:      src.Stream,
		Temperature: src.Temperature,
		TopP:        src.TopP,
	}

	if src.Thinking != nil && src.Thinking.Type == "enabled" {
		effort := "medium"
		if src.Thinking.BudgetTokens <= 1024 {
			effort = "low"
		} else if src.Thinking.BudgetTokens >= 4096 {
			effort = "high"
		}
		dst.ReasoningEffort = effort
	}

	// 1. Inject System instruction if present
	if src.System != nil {
		var sysText string
		switch sysVal := src.System.(type) {
		case string:
			sysText = sysVal
		case []any:
			var parts []string
			for _, item := range sysVal {
				if m, ok := item.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						parts = append(parts, t)
					}
				}
			}
			sysText = strings.Join(parts, "\n")
		}
		if sysText != "" {
			dst.Messages = append(dst.Messages, OpenAIMessage{
				Role:    "system",
				Content: sysText,
			})
		}
	}

	// 2. Map messages
	for _, msg := range src.Messages {
		role := msg.Role
		if role == "assistant" {
			// Extract assistant content, including potential tool use
			var textBuilder strings.Builder
			var toolCalls []OpenAIToolCall
			var reasoningBuilder strings.Builder

			for _, c := range msg.Content {
				switch c.Type {
				case "text":
					textBuilder.WriteString(c.Text)
				case "thinking":
					reasoningBuilder.WriteString(c.Thinking)
				case "tool_use":
					// Build OpenAIToolCall
					argsStr := "{}"
					if len(c.Input) > 0 {
						argsStr = string(c.Input)
					}
					toolCalls = append(toolCalls, OpenAIToolCall{
						ID:   c.ID,
						Type: "function",
						Function: OpenAIFunctionCall{
							Name:      c.Name,
							Arguments: argsStr,
						},
					})
				}
			}

			var content any
			if textBuilder.Len() > 0 {
				content = textBuilder.String()
			}
			dst.Messages = append(dst.Messages, OpenAIMessage{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: reasoningBuilder.String(),
				ToolCalls:        toolCalls,
			})

		} else {
			// User messages or tool results
			var contentParts []OpenAIContentPart
			hasImage := false

			for _, c := range msg.Content {
				switch c.Type {
				case "text":
					contentParts = append(contentParts, OpenAIContentPart{
						Type: "text",
						Text: c.Text,
					})
				case "image":
					if c.Source != nil && c.Source.Data != "" {
						hasImage = true
						mime := c.Source.MediaType
						if mime == "" {
							mime = "image/png"
						}
						data := c.Source.Data
						var url string
						if strings.HasPrefix(data, "data:") {
							url = data
						} else {
							url = fmt.Sprintf("data:%s;base64,%s", mime, data)
						}
						contentParts = append(contentParts, OpenAIContentPart{
							Type: "image_url",
							ImageURL: &OpenAIImageURL{
								URL: url,
							},
						})
					}
				case "tool_result":
					// Flush accumulated parts
					if len(contentParts) > 0 {
						var finalContent any
						if !hasImage && len(contentParts) == 1 {
							finalContent = contentParts[0].Text
						} else {
							finalContent = contentParts
						}
						dst.Messages = append(dst.Messages, OpenAIMessage{
							Role:    "user",
							Content: finalContent,
						})
						contentParts = nil
						hasImage = false
					}

					var outStr string
					switch contentVal := c.Content.(type) {
					case string:
						outStr = contentVal
					default:
						raw, _ := json.Marshal(contentVal)
						outStr = string(raw)
					}
					dst.Messages = append(dst.Messages, OpenAIMessage{
						Role:       "tool",
						Content:    outStr,
						ToolCallID: c.ToolUseID,
					})
				}
			}

			// Push remaining content parts
			if len(contentParts) > 0 {
				var finalContent any
				if !hasImage && len(contentParts) == 1 {
					finalContent = contentParts[0].Text
				} else {
					finalContent = contentParts
				}
				dst.Messages = append(dst.Messages, OpenAIMessage{
					Role:    "user",
					Content: finalContent,
				})
			}
		}
	}

	// 3. Map tools
	for _, tool := range src.Tools {
		def := OpenAITool{
			Type: "function",
			Function: OpenAIFunctionDef{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		}
		dst.Tools = append(dst.Tools, def)
	}

	// 4. Map tool choice
	if src.ToolChoice != nil {
		dst.ToolChoice = translateToolChoice(src.ToolChoice)
	}

	return dst
}

func translateOpenAIToolChoiceToAnthropic(src any) any {
	if src == nil {
		return nil
	}

	if str, ok := src.(string); ok {
		if str == "required" {
			return map[string]any{"type": "any"}
		}
		return map[string]any{"type": str}
	}

	if m, ok := src.(map[string]any); ok {
		if t, _ := m["type"].(string); t == "function" {
			if fn, ok := m["function"].(map[string]any); ok {
				if name, _ := fn["name"].(string); name != "" {
					return map[string]any{
						"type": "tool",
						"name": name,
					}
				}
			}
		}
	}

	return src
}

// TranslateOpenAIToAnthropicResponse translates a non-streaming OpenAI Chat Completion response to an Anthropic response.
func TranslateOpenAIToAnthropicResponse(src *OpenAIChatResponse, targetModel string) *AnthropicResponse {
	dst := &AnthropicResponse{
		ID:    src.ID,
		Type:  "message",
		Role:  "assistant",
		Model: targetModel,
	}

	if len(src.Choices) > 0 {
		choice := src.Choices[0]
		dst.StopReason = mapFinishReason(choice.FinishReason)

		// Map assistant message back
		msg := choice.Message
		if msg.ReasoningContent != "" {
			dst.Content = append(dst.Content, AnthropicContent{
				Type:     "thinking",
				Thinking: msg.ReasoningContent,
			})
		}
		if txt, ok := msg.Content.(string); ok && txt != "" {
			dst.Content = append(dst.Content, AnthropicContent{
				Type: "text",
				Text: txt,
			})
		}
		for _, tc := range msg.ToolCalls {
			dst.Content = append(dst.Content, AnthropicContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
	}

	dst.Usage = AnthropicUsage{
		InputTokens:  src.Usage.PromptTokens,
		OutputTokens: src.Usage.CompletionTokens,
	}

	return dst
}

// TranslateOpenAIToAnthropic maps an OpenAI Chat request to Anthropic message format.
func TranslateOpenAIToAnthropic(src *OpenAIChatRequest) *AnthropicRequest {
	dst := &AnthropicRequest{
		Model:       src.Model,
		MaxTokens:   src.MaxTokens,
		Stream:      src.Stream,
		Temperature: src.Temperature,
		TopP:        src.TopP,
	}

	if src.ReasoningEffort != "" {
		budget := 2048
		switch src.ReasoningEffort {
		case "low":
			budget = 1024
		case "medium":
			budget = 2048
		case "high":
			budget = 4096
		}
		dst.Thinking = &AnthropicThinking{
			Type:         "enabled",
			BudgetTokens: budget,
		}
	}

	var systemPrompts []string

	for _, msg := range src.Messages {
		if msg.Role == "system" || msg.Role == "developer" {
			if txt, ok := msg.Content.(string); ok {
				systemPrompts = append(systemPrompts, txt)
			}
			continue
		}

		var role string
		switch msg.Role {
		case "assistant":
			role = "assistant"
		case "user":
			role = "user"
		case "tool":
			role = "user" // Maps back to user in Anthropic's multi-turn model
		default:
			role = "user"
		}

		var contents []AnthropicContent
		if msg.Role == "tool" {
			var outStr string
			switch contentVal := msg.Content.(type) {
			case string:
				outStr = contentVal
			default:
				raw, _ := json.Marshal(contentVal)
				outStr = string(raw)
			}
			contents = append(contents, AnthropicContent{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   outStr,
			})
		} else {
			if msg.Role == "assistant" && msg.ReasoningContent != "" {
				contents = append(contents, AnthropicContent{
					Type:     "thinking",
					Thinking: msg.ReasoningContent,
				})
			}
			if txt, ok := msg.Content.(string); ok && txt != "" {
				contents = append(contents, AnthropicContent{
					Type: "text",
					Text: txt,
				})
			}
			for _, tc := range msg.ToolCalls {
				contents = append(contents, AnthropicContent{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
				})
			}
		}

		if len(contents) > 0 {
			dst.Messages = append(dst.Messages, AnthropicMessage{
				Role:    role,
				Content: contents,
			})
		}
	}

	if len(systemPrompts) > 0 {
		dst.System = strings.Join(systemPrompts, "\n")
	}

	for _, tool := range src.Tools {
		dst.Tools = append(dst.Tools, AnthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}

	if src.ToolChoice != nil {
		dst.ToolChoice = translateOpenAIToolChoiceToAnthropic(src.ToolChoice)
	}

	return dst
}

func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// ---------------------------------------------------------------------------
// OpenAI Responses API DTOs (Codex)
// ---------------------------------------------------------------------------

type CodexTool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type CodexResponsePayload struct {
	Model        string            `json:"model"`
	Input        []any             `json:"input,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Stream       *bool             `json:"stream,omitempty"`
	Store        *bool             `json:"store,omitempty"`
	Tools        []CodexTool       `json:"tools,omitempty"`
	Output       []CodexOutputItem `json:"output,omitempty"`
	Status       string            `json:"status,omitempty"`
	Usage        *CodexUsage       `json:"usage,omitempty"`
}

type CodexUsage struct {
	InputTokens        int                 `json:"input_tokens"`
	OutputTokens       int                 `json:"output_tokens"`
	InputTokensDetails *InputTokensDetails `json:"input_tokens_details,omitempty"`
}

type InputTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type CodexMessage struct {
	Role    string             `json:"role"`
	Content []AnthropicContent `json:"content"`
}

type CodexOutputItem struct {
	Type         string                `json:"type"` // "message", "reasoning", "function_call"
	Content      []CodexContentBlock   `json:"content,omitempty"`
	Summary      []CodexContentBlock   `json:"summary,omitempty"`
	CallID       string                `json:"call_id,omitempty"`
	Name         string                `json:"name,omitempty"`
	Arguments    string                `json:"arguments,omitempty"`
}

type CodexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// TranslateAnthropicToResponses translates an Anthropic Messages request to OpenAI Responses request format.
func TranslateAnthropicToResponses(src *AnthropicRequest) *CodexResponsePayload {
	storeVal := false
	dst := &CodexResponsePayload{
		Model:  src.Model,
		Stream: &src.Stream,
		Store:  &storeVal,
	}

	for _, tool := range src.Tools {
		params := tool.InputSchema
		if len(params) == 0 {
			params = json.RawMessage(`{"type": "object", "properties": {}}`)
		}
		dst.Tools = append(dst.Tools, CodexTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  params,
		})
	}

	if src.System != nil {
		switch sysVal := src.System.(type) {
		case string:
			dst.Instructions = sysVal
		}
	}

	var inputItems []any

	for _, msg := range src.Messages {
		role := msg.Role
		var textParts []any

		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				typ := "input_text"
				if role == "assistant" {
					typ = "output_text"
				}
				textParts = append(textParts, map[string]any{
					"type": typ,
					"text": block.Text,
				})

			case "image":
				mime := block.Source.MediaType
				if mime == "" {
					mime = "image/png"
				}
				data := block.Source.Data
				var imageURL string
				if strings.HasPrefix(data, "data:") {
					imageURL = data
				} else {
					imageURL = fmt.Sprintf("data:%s;base64,%s", mime, data)
				}
				textParts = append(textParts, map[string]any{
					"type":      "input_image",
					"image_url": imageURL,
				})

			case "tool_use":
				if len(textParts) > 0 {
					inputItems = append(inputItems, map[string]any{
						"role":    role,
						"content": textParts,
					})
					textParts = nil
				}
				inputItems = append(inputItems, map[string]any{
					"type":      "function_call",
					"call_id":   block.ID,
					"name":      block.Name,
					"arguments": string(block.Input),
				})

			case "tool_result":
				if len(textParts) > 0 {
					inputItems = append(inputItems, map[string]any{
						"role":    role,
						"content": textParts,
					})
					textParts = nil
				}
				var outStr string
				if block.Content != nil {
					outStr = stringifyToolResultContent(block.Content)
				} else {
					outStr = block.Text
				}
				inputItems = append(inputItems, map[string]any{
					"type":    "function_call_output",
					"call_id": block.ToolUseID,
					"output":  outStr,
				})
			}
		}

		if len(textParts) > 0 {
			inputItems = append(inputItems, map[string]any{
				"role":    role,
				"content": textParts,
			})
		}
	}
	dst.Input = inputItems

	return dst
}

func stringifyToolResultContent(content any) string {
	if content == nil {
		return ""
	}
	switch val := content.(type) {
	case string:
		return val
	case []any:
		var parts []string
		for _, cVal := range val {
			if cMap, ok := cVal.(map[string]any); ok {
				if cText, ok := cMap["text"].(string); ok {
					parts = append(parts, cText)
				} else {
					b, _ := json.Marshal(cMap)
					parts = append(parts, string(b))
				}
			} else {
				parts = append(parts, fmt.Sprint(cVal))
			}
		}
		return strings.Join(parts, "")
	case []AnthropicContent:
		var parts []string
		for _, c := range val {
			if c.Type == "text" {
				parts = append(parts, c.Text)
			} else {
				b, _ := json.Marshal(c)
				parts = append(parts, string(b))
			}
		}
		return strings.Join(parts, "")
	default:
		return fmt.Sprint(val)
	}
}

// TranslateResponsesToAnthropicMessage converts a Codex Responses API payload back to Anthropic response.
func TranslateResponsesToAnthropicMessage(src *CodexResponsePayload, model string) *AnthropicResponse {
	dst := &AnthropicResponse{
		ID:           "msg_" + strings.ReplaceAll(uuid.New().String(), "-", ""),
		Type:         "message",
		Role:         "assistant",
		Model:        model,
		StopReason:   "end_turn",
		StopSequence: "",
	}

	for _, out := range src.Output {
		if out.Type == "reasoning" {
			var parts []string
			for _, s := range out.Summary {
				if s.Type == "summary_text" {
					parts = append(parts, s.Text)
				}
			}
			t := strings.Join(parts, "")
			if t != "" {
				dst.Content = append(dst.Content, AnthropicContent{
					Type:     "thinking",
					Thinking: t,
				})
			}
		} else if out.Type == "message" {
			var parts []string
			for _, c := range out.Content {
				if c.Type == "output_text" {
					parts = append(parts, c.Text)
				}
			}
			t := strings.Join(parts, "")
			if t != "" {
				dst.Content = append(dst.Content, AnthropicContent{
					Type: "text",
					Text: t,
				})
			}
		} else if out.Type == "function_call" {
			dst.Content = append(dst.Content, AnthropicContent{
				Type:  "tool_use",
				ID:    out.CallID,
				Name:  out.Name,
				Input: json.RawMessage(out.Arguments),
			})
		}
	}

	hasToolUse := false
	for _, c := range dst.Content {
		if c.Type == "tool_use" {
			hasToolUse = true
			break
		}
	}

	if src.Status == "incomplete" {
		dst.StopReason = "max_tokens"
	} else if hasToolUse {
		dst.StopReason = "tool_use"
	}

	if src.Usage != nil {
		cached := 0
		if src.Usage.InputTokensDetails != nil {
			cached = src.Usage.InputTokensDetails.CachedTokens
		}
		dst.Usage = AnthropicUsage{
			InputTokens:              src.Usage.InputTokens,
			OutputTokens:             src.Usage.OutputTokens,
			CacheCreationInputTokens: cached,
			CacheReadInputTokens:     cached,
		}
	}

	return dst
}

// ---------------------------------------------------------------------------
// Streaming translation helpers
// ---------------------------------------------------------------------------

// TranslateOpenAIChunkToAnthropic translates an OpenAI stream chunk to Anthropic Server-Sent Events (SSE).
func TranslateOpenAIChunkToAnthropic(chunk *OpenAIStreamChunk) ([]string, string, string) {
	if len(chunk.Choices) == 0 {
		return nil, "", ""
	}
	choice := chunk.Choices[0]
	var events []string

	// Check if there is delta content
	if choice.Delta.Content != "" {
		eventData := fmt.Sprintf(`{"type": "content_block_delta", "index": %d, "delta": {"type": "text_delta", "text": %s}}`,
			choice.Index, stringifyJSON(choice.Delta.Content))
		events = append(events, "content_block_delta", eventData)
	}

	// Check if there is reasoning content (thinking)
	if choice.Delta.ReasoningContent != "" {
		eventData := fmt.Sprintf(`{"type": "content_block_delta", "index": %d, "delta": {"type": "thinking_delta", "thinking": %s}}`,
			choice.Index, stringifyJSON(choice.Delta.ReasoningContent))
		events = append(events, "content_block_delta", eventData)
	}

	// Handle tool calls
	for _, tc := range choice.Delta.ToolCalls {
		if tc.Function.Name != "" {
			// Block start for tool call
			startData := fmt.Sprintf(`{"type": "content_block_start", "index": %d, "content_block": {"type": "tool_use", "id": %s, "name": %s, "input": {}}}`,
				choice.Index, stringifyJSON(tc.ID), stringifyJSON(tc.Function.Name))
			events = append(events, "content_block_start", startData)
		}
		if tc.Function.Arguments != "" {
			// Argument delta
			deltaData := fmt.Sprintf(`{"type": "content_block_delta", "index": %d, "delta": {"type": "input_json_delta", "partial_json": %s}}`,
				choice.Index, stringifyJSON(tc.Function.Arguments))
			events = append(events, "content_block_delta", deltaData)
		}
	}

	var stopReason, stopSeq string
	if choice.FinishReason != "" {
		stopReason = mapFinishReason(choice.FinishReason)
		// Close block and stop message
		events = append(events, "message_delta", fmt.Sprintf(`{"type": "message_delta", "delta": {"stop_reason": %s}}`, stringifyJSON(stopReason)))
		events = append(events, "message_stop", `{"type": "message_stop"}`)
	}

	return events, stopReason, stopSeq
}

// TranslateAnthropicSSEToOpenAI translates Anthropic stream events to OpenAI compatible chunk format.
func TranslateAnthropicSSEToOpenAI(event string, data string, chatID string, model string) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		return "", err
	}

	evtType, _ := payload["type"].(string)
	delta := map[string]any{}
	var finishReason any = nil

	switch evtType {
	case "content_block_delta":
		dMap, _ := payload["delta"].(map[string]any)
		if dMap != nil {
			if txt, ok := dMap["text"].(string); ok {
				delta["content"] = txt
			}
			if thinking, ok := dMap["thinking"].(string); ok {
				delta["reasoning_content"] = thinking
			}
			if pJson, ok := dMap["partial_json"].(string); ok {
				// Tool call delta format mapping
				idxVal, _ := payload["index"].(float64)
				delta["tool_calls"] = []any{
					map[string]any{
						"index": int(idxVal),
						"function": map[string]any{
							"arguments": pJson,
						},
					},
				}
			}
		}
	case "content_block_start":
		block, _ := payload["content_block"].(map[string]any)
		if block != nil {
			if block["type"] == "tool_use" {
				idxVal, _ := payload["index"].(float64)
				delta["tool_calls"] = []any{
					map[string]any{
						"index": int(idxVal),
						"id":    block["id"],
						"type":  "function",
						"function": map[string]any{
							"name":      block["name"],
							"arguments": "",
						},
					},
				}
			}
		}
	case "message_delta":
		dMap, _ := payload["delta"].(map[string]any)
		if dMap != nil {
			if reason, ok := dMap["stop_reason"].(string); ok {
				finishReason = mapAnthropicStopReasonToOpenAI(reason)
			}
		}
	case "message_stop":
		return "data: [DONE]\n\n", nil
	default:
		return "", nil
	}

	// Format as OpenAI Chat Completion Chunk
	chunk := map[string]any{
		"id":      chatID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}

	raw, _ := json.Marshal(chunk)
	return fmt.Sprintf("data: %s\n\n", string(raw)), nil
}

func mapAnthropicStopReasonToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func stringifyJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
