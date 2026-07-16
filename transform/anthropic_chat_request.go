package transform

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/anthropic"
	"github.com/bizshuk/proxy/protocol/chat"
)

// AnthropicToChatRequest converts an Anthropic Messages request to Chat Completions.
func AnthropicToChatRequest(_ context.Context, env protocol.RequestEnvelope) (protocol.TransformResult, error) {
	src, err := anthropic.DecodeRequest(env.Body)
	if err != nil {
		return protocol.TransformResult{}, invalidRequest("decode anthropic request", err)
	}
	dst := chat.Request{
		Model:       env.Model,
		Stream:      env.Stream,
		MaxTokens:   src.MaxTokens,
		Temperature: src.Temperature,
		TopP:        src.TopP,
	}
	if err := appendAnthropicSystem(&dst, src.System); err != nil {
		return protocol.TransformResult{}, err
	}
	if err := appendAnthropicMessages(&dst, src.Messages); err != nil {
		return protocol.TransformResult{}, err
	}
	if err := mapAnthropicTools(&dst, src.Tools, src.ToolChoice); err != nil {
		return protocol.TransformResult{}, err
	}
	if len(src.StopSequences) > 0 {
		dst.Stop, err = json.Marshal(src.StopSequences)
		if err != nil {
			return protocol.TransformResult{}, fmt.Errorf("encode chat stop sequences: %w", err)
		}
	}
	dst.ReasoningEffort = reasoningEffort(src.Thinking)
	body, err := chat.Encode(dst)
	if err != nil {
		return protocol.TransformResult{}, fmt.Errorf("encode chat request: %w", err)
	}
	return protocol.TransformResult{Body: body, Losses: thinkingLoss(src.Thinking)}, nil
}

// ChatToAnthropicRequest converts a Chat Completions request to Anthropic Messages.
func ChatToAnthropicRequest(_ context.Context, env protocol.RequestEnvelope) (protocol.TransformResult, error) {
	src, err := chat.DecodeRequest(env.Body)
	if err != nil {
		return protocol.TransformResult{}, invalidRequest("decode chat request", err)
	}
	dst := anthropic.Request{
		Model:       env.Model,
		Stream:      env.Stream,
		Temperature: src.Temperature,
		TopP:        src.TopP,
	}
	if src.MaxCompletionTokens > 0 {
		dst.MaxTokens = src.MaxCompletionTokens
	} else {
		dst.MaxTokens = src.MaxTokens
	}

	losses := make([]protocol.SemanticLoss, 0, 3)
	for _, message := range src.Messages {
		switch message.Role {
		case "system", "developer":
			blocks, err := chatContentToAnthropic(message.Content)
			if err != nil {
				return protocol.TransformResult{}, err
			}
			for _, block := range blocks {
				if block.Type != "text" {
					return protocol.TransformResult{}, unsupportedFeature("messages.content", "Anthropic system blocks support only text when translating Chat instructions")
				}
				dst.System = append(dst.System, block)
			}
			if message.Role == "developer" {
				losses = append(losses, protocol.SemanticLoss{
					Field:  "messages.role",
					Reason: "Anthropic system blocks do not preserve Chat developer priority",
				})
			}
		default:
			translated, messageLosses, err := chatMessageToAnthropic(message)
			if err != nil {
				return protocol.TransformResult{}, err
			}
			dst.Messages = append(dst.Messages, translated)
			losses = append(losses, messageLosses...)
		}
	}

	if err := mapChatTools(&dst, src.Tools, src.ToolChoice); err != nil {
		return protocol.TransformResult{}, err
	}
	dst.StopSequences, err = chatStopSequences(src.Stop)
	if err != nil {
		return protocol.TransformResult{}, err
	}
	if src.ReasoningEffort != "" {
		budget, err := reasoningBudget(src.ReasoningEffort)
		if err != nil {
			return protocol.TransformResult{}, err
		}
		dst.Thinking = &anthropic.Thinking{Type: "enabled", BudgetTokens: budget}
		losses = append(losses, protocol.SemanticLoss{
			Field:  "reasoning_effort",
			Reason: "Anthropic thinking requires a token budget rather than a low/medium/high bucket",
		})
	}
	if src.ParallelToolCalls != nil {
		losses = append(losses, protocol.SemanticLoss{
			Field:  "parallel_tool_calls",
			Reason: "Anthropic Messages does not preserve the Chat parallel tool call preference",
		})
	}

	body, err := anthropic.Encode(dst)
	if err != nil {
		return protocol.TransformResult{}, fmt.Errorf("encode anthropic request: %w", err)
	}
	return protocol.TransformResult{Body: body, Losses: losses}, nil
}

func appendAnthropicSystem(dst *chat.Request, system anthropic.ContentList) error {
	for _, block := range system {
		if block.Type != "text" {
			return unsupportedFeature("system", fmt.Sprintf("Anthropic system content type %q cannot be represented in Chat", block.Type))
		}
		dst.Messages = append(dst.Messages, chat.Message{Role: "system", Content: chat.TextContent(block.Text)})
	}
	return nil
}

func appendAnthropicMessages(dst *chat.Request, messages []anthropic.Message) error {
	toolNames := make(map[string]string)
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			return unsupportedFeature("messages.role", fmt.Sprintf("Anthropic role %q cannot be represented in Chat", message.Role))
		}

		var regular []anthropic.Content
		flushRegular := func() error {
			if len(regular) == 0 {
				return nil
			}
			translated, err := anthropicContentToChat(message.Role, regular, toolNames)
			if err != nil {
				return err
			}
			dst.Messages = append(dst.Messages, translated)
			regular = nil
			return nil
		}

		for _, block := range message.Content {
			if block.Type != "tool_result" {
				regular = append(regular, block)
				continue
			}
			if err := flushRegular(); err != nil {
				return err
			}
			content, err := anthropicToolResultToChat(block.Content)
			if err != nil {
				return err
			}
			dst.Messages = append(dst.Messages, chat.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: block.ToolUseID,
				Name:       toolNames[block.ToolUseID],
			})
		}
		if err := flushRegular(); err != nil {
			return err
		}
	}
	return nil
}

func anthropicContentToChat(role string, blocks []anthropic.Content, toolNames map[string]string) (chat.Message, error) {
	message := chat.Message{Role: role}
	var contentBlocks []anthropic.Content
	var reasoning strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case "text", "image":
			contentBlocks = append(contentBlocks, block)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			if role != "assistant" {
				return chat.Message{}, unsupportedFeature("messages.content", "Anthropic tool_use is only representable on assistant messages")
			}
			arguments, err := compactJSONObject(block.Input)
			if err != nil {
				return chat.Message{}, invalidRequest("decode Anthropic tool input", err)
			}
			message.ToolCalls = append(message.ToolCalls, chat.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: chat.FunctionCall{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
			toolNames[block.ID] = block.Name
		case "tool_result":
			return chat.Message{}, unsupportedFeature("messages.content", "nested Anthropic tool_result cannot be represented in a Chat message")
		default:
			return chat.Message{}, unsupportedFeature("messages.content", fmt.Sprintf("Anthropic content type %q cannot be represented in Chat", block.Type))
		}
	}
	content, err := anthropicBlocksToChatContent(contentBlocks)
	if err != nil {
		return chat.Message{}, err
	}
	message.Content = content
	message.ReasoningContent = reasoning.String()
	return message, nil
}

func compactJSONObject(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func anthropicBlocksToChatContent(blocks []anthropic.Content) (*chat.MessageContent, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	textOnly := true
	var text strings.Builder
	for _, block := range blocks {
		if block.Type != "text" {
			textOnly = false
			break
		}
		text.WriteString(block.Text)
	}
	if textOnly {
		return chat.TextContent(text.String()), nil
	}

	parts := make([]chat.ContentPart, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, chat.ContentPart{Type: "text", Text: block.Text})
		case "image":
			if block.Source == nil || block.Source.Type != "base64" {
				return nil, unsupportedFeature("messages.content.source", "only Anthropic base64 images can be represented in Chat")
			}
			parts = append(parts, chat.ContentPart{
				Type: "image_url",
				ImageURL: &chat.ImageURL{
					URL: "data:" + block.Source.MediaType + ";base64," + block.Source.Data,
				},
			})
		default:
			return nil, unsupportedFeature("messages.content", fmt.Sprintf("Anthropic content type %q cannot be represented in Chat", block.Type))
		}
	}
	return chat.PartsContent(parts), nil
}

func anthropicToolResultToChat(raw json.RawMessage) (*chat.MessageContent, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return chat.TextContent(""), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return chat.TextContent(text), nil
	}
	var blocks []anthropic.Content
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, unsupportedFeature("messages.content.tool_result", "Anthropic tool_result content must be text or supported content blocks")
	}
	return anthropicBlocksToChatContent(blocks)
}

func mapAnthropicTools(dst *chat.Request, tools []anthropic.Tool, choice *anthropic.ToolChoice) error {
	for _, tool := range tools {
		dst.Tools = append(dst.Tools, chat.Tool{
			Type: "function",
			Function: chat.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	if choice == nil {
		return nil
	}
	switch choice.Type {
	case "auto":
		dst.ToolChoice = json.RawMessage(`"auto"`)
	case "any":
		dst.ToolChoice = json.RawMessage(`"required"`)
	case "tool":
		body, err := json.Marshal(struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}{Type: "function", Function: struct {
			Name string `json:"name"`
		}{Name: choice.Name}})
		if err != nil {
			return fmt.Errorf("encode chat tool choice: %w", err)
		}
		dst.ToolChoice = body
	default:
		return unsupportedFeature("tool_choice", fmt.Sprintf("Anthropic tool choice %q cannot be represented in Chat", choice.Type))
	}
	return nil
}

func chatMessageToAnthropic(message chat.Message) (anthropic.Message, []protocol.SemanticLoss, error) {
	var role string
	switch message.Role {
	case "user":
		role = "user"
	case "assistant":
		role = "assistant"
	case "tool":
		content, err := chatToolResultContent(message.Content)
		if err != nil {
			return anthropic.Message{}, nil, err
		}
		losses := nameLoss(message.Name)
		return anthropic.Message{
			Role: "user",
			Content: anthropic.ContentList{{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Content:   content,
			}},
		}, losses, nil
	default:
		return anthropic.Message{}, nil, unsupportedFeature("messages.role", fmt.Sprintf("Chat role %q cannot be represented in Anthropic Messages", message.Role))
	}

	blocks, err := chatContentToAnthropic(message.Content)
	if err != nil {
		return anthropic.Message{}, nil, err
	}
	if message.ReasoningContent != "" {
		blocks = append(anthropic.ContentList{{Type: "thinking", Thinking: message.ReasoningContent}}, blocks...)
	}
	for _, call := range message.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			return anthropic.Message{}, nil, unsupportedFeature("messages.tool_calls.type", fmt.Sprintf("Chat tool call type %q cannot be represented in Anthropic Messages", call.Type))
		}
		input := json.RawMessage(call.Function.Arguments)
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		if !json.Valid(input) {
			return anthropic.Message{}, nil, invalidRequest("decode Chat tool call arguments", fmt.Errorf("tool call %q arguments are not valid JSON", call.ID))
		}
		blocks = append(blocks, anthropic.Content{
			Type:  "tool_use",
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: input,
		})
	}
	return anthropic.Message{Role: role, Content: blocks}, nameLoss(message.Name), nil
}

func chatContentToAnthropic(content *chat.MessageContent) (anthropic.ContentList, error) {
	if content == nil {
		return anthropic.ContentList{}, nil
	}
	if content.Parts == nil {
		return anthropic.ContentList{{Type: "text", Text: content.Text}}, nil
	}
	blocks := make(anthropic.ContentList, 0, len(content.Parts))
	for _, part := range content.Parts {
		switch part.Type {
		case "text":
			blocks = append(blocks, anthropic.Content{Type: "text", Text: part.Text})
		case "image_url":
			if part.ImageURL == nil {
				return nil, invalidRequest("decode Chat image", fmt.Errorf("image_url payload is required"))
			}
			source, err := dataURLSource(part.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, anthropic.Content{Type: "image", Source: source})
		default:
			return nil, unsupportedFeature("messages.content", fmt.Sprintf("Chat content part %q cannot be represented in Anthropic Messages", part.Type))
		}
	}
	return blocks, nil
}

func chatToolResultContent(content *chat.MessageContent) (json.RawMessage, error) {
	if content == nil {
		return json.RawMessage(`""`), nil
	}
	if content.Parts == nil {
		body, err := json.Marshal(content.Text)
		if err != nil {
			return nil, fmt.Errorf("encode Anthropic tool result text: %w", err)
		}
		return body, nil
	}
	blocks, err := chatContentToAnthropic(content)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(blocks)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic tool result blocks: %w", err)
	}
	return body, nil
}

func mapChatTools(dst *anthropic.Request, tools []chat.Tool, rawChoice json.RawMessage) error {
	for _, tool := range tools {
		if tool.Type != "function" {
			return unsupportedFeature("tools.type", fmt.Sprintf("Chat tool type %q cannot be represented in Anthropic Messages", tool.Type))
		}
		dst.Tools = append(dst.Tools, anthropic.Tool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	if len(rawChoice) == 0 || string(rawChoice) == "null" {
		return nil
	}
	var choice string
	if err := json.Unmarshal(rawChoice, &choice); err == nil {
		switch choice {
		case "auto":
			dst.ToolChoice = &anthropic.ToolChoice{Type: "auto"}
		case "required":
			dst.ToolChoice = &anthropic.ToolChoice{Type: "any"}
		default:
			return unsupportedFeature("tool_choice", fmt.Sprintf("Chat tool choice %q cannot be represented in Anthropic Messages", choice))
		}
		return nil
	}
	var named struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(rawChoice, &named); err != nil {
		return invalidRequest("decode Chat tool choice", err)
	}
	if named.Type != "function" || named.Function.Name == "" {
		return unsupportedFeature("tool_choice", "Chat named tool choice must select a function")
	}
	dst.ToolChoice = &anthropic.ToolChoice{Type: "tool", Name: named.Function.Name}
	return nil
}

func chatStopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, invalidRequest("decode Chat stop", err)
	}
	return multiple, nil
}

func dataURLSource(value string) (*anthropic.Source, error) {
	if !strings.HasPrefix(value, "data:") {
		return nil, unsupportedFeature("messages.content.image_url", "Anthropic Messages accepts inline base64 images, not remote image URLs")
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ";base64,")
	if !ok || header == "" || data == "" {
		return nil, invalidRequest("decode Chat image data URL", fmt.Errorf("expected data:<media-type>;base64,<data>"))
	}
	if _, err := base64.StdEncoding.DecodeString(data); err != nil {
		return nil, invalidRequest("decode Chat image data URL", err)
	}
	return &anthropic.Source{Type: "base64", MediaType: header, Data: data}, nil
}

func reasoningEffort(value *anthropic.Thinking) string {
	if value == nil || value.BudgetTokens == 0 {
		return ""
	}
	if value.BudgetTokens <= 1024 {
		return "low"
	}
	if value.BudgetTokens < 4096 {
		return "medium"
	}
	return "high"
}

func reasoningBudget(effort string) (int, error) {
	switch effort {
	case "low":
		return 1024, nil
	case "medium":
		return 2048, nil
	case "high":
		return 4096, nil
	default:
		return 0, unsupportedFeature("reasoning_effort", fmt.Sprintf("Chat reasoning effort %q cannot be represented in Anthropic Messages", effort))
	}
}

func nameLoss(name string) []protocol.SemanticLoss {
	if name == "" {
		return nil
	}
	return []protocol.SemanticLoss{{
		Field:  "messages.name",
		Reason: "Anthropic Messages does not preserve the Chat message name field",
	}}
}

func invalidRequest(operation string, err error) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: operation + ": " + err.Error(),
		Cause:   err,
	}
}

func unsupportedFeature(field, message string) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    "unsupported_feature",
		Message: field + ": " + message,
	}
}

func thinkingLoss(value *anthropic.Thinking) []protocol.SemanticLoss {
	if value == nil || value.BudgetTokens == 0 {
		return nil
	}
	return []protocol.SemanticLoss{{
		Field:  "thinking.budget_tokens",
		Reason: "OpenAI reasoning_effort preserves only a low/medium/high bucket",
	}}
}
