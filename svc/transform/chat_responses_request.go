package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/chat"
	"github.com/bizshuk/proxy/model/responses"
)

const (
	chatResponsesCodeInvalidRequest         = "invalid_request"
	chatResponsesCodeUnsupportedContent     = "unsupported_content"
	chatResponsesCodeUnsupportedMessageRole = "unsupported_message_role"
	chatResponsesCodeUnsupportedTool        = "unsupported_tool"
	chatResponsesCodeStatefulContext        = "stateful_context_not_portable"
)

// ChatToResponsesRequest converts an OpenAI Chat Completions request to an
// OpenAI Responses request without routing through a third wire format.
func ChatToResponsesRequest(ctx context.Context, envelope model.RequestEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := chat.DecodeRequest(envelope.Body)
	if err != nil {
		return model.TransformResult{}, chatResponsesInvalidRequest("decode Chat request", err)
	}

	stream := envelope.Stream
	target := responses.Request{
		Model:             envelope.Model,
		Stream:            &stream,
		ParallelToolCalls: source.ParallelToolCalls,
	}
	var (
		instructions []string
		input        []responses.InputItem
		losses       []model.SemanticLoss
		developer    bool
	)

	for index, message := range source.Messages {
		switch message.Role {
		case "system", "developer":
			text, messageLosses, err := chatResponsesInstructionText(message)
			if err != nil {
				return model.TransformResult{}, fmt.Errorf("convert Chat messages[%d]: %w", index, err)
			}
			instructions = append(instructions, text)
			losses = append(losses, messageLosses...)
			if message.Name != "" {
				losses = append(losses, chatResponsesNameLoss(index))
			}
			developer = developer || message.Role == "developer"
		case "user", "assistant":
			items, messageLosses, err := chatResponsesMessageItems(message)
			if err != nil {
				return model.TransformResult{}, fmt.Errorf("convert Chat messages[%d]: %w", index, err)
			}
			input = append(input, items...)
			losses = append(losses, messageLosses...)
			if message.Name != "" {
				losses = append(losses, chatResponsesNameLoss(index))
			}
		case "tool":
			if message.ToolCallID == "" {
				return model.TransformResult{}, chatResponsesInvalidRequest(
					fmt.Sprintf("convert Chat messages[%d]", index),
					fmt.Errorf("tool_call_id is required for tool messages"),
				)
			}
			output, err := chatResponsesPlainText(message.Content)
			if err != nil {
				return model.TransformResult{}, fmt.Errorf("convert Chat messages[%d]: %w", index, err)
			}
			input = append(input, responses.InputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: output,
			})
		default:
			return model.TransformResult{}, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedMessageRole,
				fmt.Sprintf("Chat message role %q cannot be represented by Responses", message.Role),
			)
		}
	}
	if developer {
		losses = append(losses, model.SemanticLoss{
			Field:  "messages.role",
			Reason: "Responses instructions do not preserve Chat developer priority",
		})
	}
	losses = append(losses, chatResponsesRequestLosses(source)...)

	if len(input) == 0 {
		input = []responses.InputItem{}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode Responses input: %w", err)
	}
	target.Input = inputJSON
	target.Instructions = strings.Join(instructions, "\n")
	target.Tools, err = chatResponsesTools(source.Tools)
	if err != nil {
		return model.TransformResult{}, err
	}
	target.ToolChoice, err = chatResponsesToolChoice(source.ToolChoice)
	if err != nil {
		return model.TransformResult{}, err
	}
	if source.ReasoningEffort != "" {
		target.Reasoning = &responses.Reasoning{Effort: source.ReasoningEffort}
	}

	body, err := responses.Encode(target)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode Responses request: %w", err)
	}
	return model.TransformResult{Body: body, Losses: losses}, nil
}

// ResponsesToChatRequest converts an OpenAI Responses request to an OpenAI
// Chat Completions request without routing through a third wire format.
func ResponsesToChatRequest(ctx context.Context, envelope model.RequestEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := responses.DecodeRequest(envelope.Body)
	if err != nil {
		return model.TransformResult{}, chatResponsesInvalidRequest("decode Responses request", err)
	}
	if source.PreviousResponseID != "" {
		return model.TransformResult{}, chatResponsesUnsupported(
			chatResponsesCodeStatefulContext,
			"previous_response_id cannot be reproduced by Chat Completions without provider-side history",
		)
	}

	target := chat.Request{
		Model:             envelope.Model,
		Stream:            envelope.Stream,
		ParallelToolCalls: source.ParallelToolCalls,
	}
	var losses []model.SemanticLoss
	if source.Instructions != "" {
		target.Messages = append(target.Messages, chat.Message{
			Role:    "system",
			Content: chat.TextContent(source.Instructions),
		})
	}
	if source.Store != nil {
		losses = append(losses, model.SemanticLoss{
			Field:  "store",
			Reason: "Chat Completions does not expose Responses storage behavior",
		})
	}

	input, err := responses.DecodeInput(source.Input)
	if err != nil {
		return model.TransformResult{}, chatResponsesInvalidRequest("decode Responses input", err)
	}
	callNames := make(map[string]string)
	for index, item := range input {
		if item.ID != "" {
			losses = append(losses, model.SemanticLoss{
				Field:  fmt.Sprintf("input[%d].id", index),
				Reason: "Chat Completions messages do not carry Responses item IDs",
			})
		}
		switch item.Type {
		case "message":
			message, err := chatResponsesInputMessage(item)
			if err != nil {
				return model.TransformResult{}, fmt.Errorf("convert Responses input[%d]: %w", index, err)
			}
			target.Messages = append(target.Messages, message)
		case "function_call":
			if item.CallID == "" || item.Name == "" {
				return model.TransformResult{}, chatResponsesInvalidRequest(
					fmt.Sprintf("convert Responses input[%d]", index),
					fmt.Errorf("function_call requires call_id and name"),
				)
			}
			callNames[item.CallID] = item.Name
			call := chat.ToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: chat.FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			}
			chatResponsesAppendToolCall(&target.Messages, call)
		case "function_call_output":
			if item.CallID == "" {
				return model.TransformResult{}, chatResponsesInvalidRequest(
					fmt.Sprintf("convert Responses input[%d]", index),
					fmt.Errorf("function_call_output requires call_id"),
				)
			}
			target.Messages = append(target.Messages, chat.Message{
				Role:       "tool",
				Content:    chat.TextContent(item.Output),
				ToolCallID: item.CallID,
				Name:       callNames[item.CallID],
			})
		default:
			return model.TransformResult{}, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedContent,
				fmt.Sprintf("Responses input type %q cannot be represented by Chat Completions", item.Type),
			)
		}
	}

	target.Tools, err = chatResponsesChatTools(source.Tools)
	if err != nil {
		return model.TransformResult{}, err
	}
	target.ToolChoice, err = chatResponsesChatToolChoice(source.ToolChoice)
	if err != nil {
		return model.TransformResult{}, err
	}
	if source.Reasoning != nil {
		target.ReasoningEffort = source.Reasoning.Effort
		if source.Reasoning.Summary != "" {
			losses = append(losses, model.SemanticLoss{
				Field:  "reasoning.summary",
				Reason: "Chat Completions does not configure reasoning summary verbosity",
			})
		}
	}

	body, err := chat.Encode(target)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode Chat request: %w", err)
	}
	return model.TransformResult{Body: body, Losses: losses}, nil
}

func chatResponsesInstructionText(message chat.Message) (string, []model.SemanticLoss, error) {
	if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
		return "", nil, chatResponsesUnsupported(
			chatResponsesCodeUnsupportedContent,
			"instruction messages cannot contain tool calls",
		)
	}
	text, err := chatResponsesPlainText(message.Content)
	return text, nil, err
}

func chatResponsesPlainText(content *chat.MessageContent) (string, error) {
	if content == nil {
		return "", nil
	}
	if content.Parts == nil {
		return content.Text, nil
	}
	var texts []string
	for _, part := range content.Parts {
		if part.Type != "text" {
			return "", chatResponsesUnsupported(
				chatResponsesCodeUnsupportedContent,
				fmt.Sprintf("content type %q is not plain text", part.Type),
			)
		}
		texts = append(texts, part.Text)
	}
	return strings.Join(texts, ""), nil
}

func chatResponsesMessageItems(message chat.Message) ([]responses.InputItem, []model.SemanticLoss, error) {
	if message.ToolCallID != "" {
		return nil, nil, chatResponsesInvalidRequest("convert Chat message", fmt.Errorf("tool_call_id is only valid for tool messages"))
	}
	var (
		items  []responses.InputItem
		losses []model.SemanticLoss
	)
	if message.ReasoningContent != "" {
		losses = append(losses, model.SemanticLoss{
			Field:  "messages.reasoning_content",
			Reason: "Responses request input does not expose Chat reasoning_content",
		})
	}
	content, contentLosses, err := chatResponsesContent(message.Role, message.Content)
	if err != nil {
		return nil, nil, err
	}
	losses = append(losses, contentLosses...)
	if message.Content != nil {
		items = append(items, responses.InputItem{
			Type:    "message",
			Role:    message.Role,
			Content: content,
		})
	}
	if message.Role != "assistant" && len(message.ToolCalls) > 0 {
		return nil, nil, chatResponsesInvalidRequest("convert Chat message", fmt.Errorf("tool_calls require assistant role"))
	}
	for _, toolCall := range message.ToolCalls {
		if toolCall.ID == "" || toolCall.Type != "function" || toolCall.Function.Name == "" {
			return nil, nil, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedTool,
				"Responses supports only named function tool calls with stable IDs",
			)
		}
		items = append(items, responses.InputItem{
			Type:      "function_call",
			CallID:    toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: toolCall.Function.Arguments,
		})
	}
	return items, losses, nil
}

func chatResponsesRequestLosses(source *chat.Request) []model.SemanticLoss {
	losses := make([]model.SemanticLoss, 0, 5)
	if source.MaxTokens != 0 {
		losses = append(losses, model.SemanticLoss{
			Field:  "max_tokens",
			Reason: "The current Responses request contract does not expose a Chat token cap",
		})
	}
	if source.MaxCompletionTokens != 0 {
		losses = append(losses, model.SemanticLoss{
			Field:  "max_completion_tokens",
			Reason: "The current Responses request contract does not expose a Chat completion token cap",
		})
	}
	if source.Temperature != nil {
		losses = append(losses, model.SemanticLoss{
			Field:  "temperature",
			Reason: "The current Responses request contract does not expose Chat temperature",
		})
	}
	if source.TopP != nil {
		losses = append(losses, model.SemanticLoss{
			Field:  "top_p",
			Reason: "The current Responses request contract does not expose Chat top_p",
		})
	}
	if len(bytes.TrimSpace(source.Stop)) != 0 {
		losses = append(losses, model.SemanticLoss{
			Field:  "stop",
			Reason: "Responses requests do not expose Chat stop sequences",
		})
	}
	return losses
}

func chatResponsesNameLoss(index int) model.SemanticLoss {
	return model.SemanticLoss{
		Field:  fmt.Sprintf("messages[%d].name", index),
		Reason: "Responses message items do not preserve Chat message names",
	}
}

func chatResponsesContent(role string, content *chat.MessageContent) (responses.ContentList, []model.SemanticLoss, error) {
	if content == nil {
		return nil, nil, nil
	}
	textType := "input_text"
	if role == "assistant" {
		textType = "output_text"
	}
	if content.Parts == nil {
		return responses.ContentList{{Type: textType, Text: content.Text}}, nil, nil
	}

	parts := make(responses.ContentList, 0, len(content.Parts))
	var losses []model.SemanticLoss
	for _, part := range content.Parts {
		switch part.Type {
		case "text":
			parts = append(parts, responses.Content{Type: textType, Text: part.Text})
		case "image_url":
			if part.ImageURL == nil || part.ImageURL.URL == "" {
				return nil, nil, chatResponsesInvalidRequest("convert Chat image", fmt.Errorf("image_url.url is required"))
			}
			parts = append(parts, responses.Content{Type: "input_image", ImageURL: part.ImageURL.URL})
			if part.ImageURL.Detail != "" {
				losses = append(losses, model.SemanticLoss{
					Field:  "messages.content.image_url.detail",
					Reason: "Responses input_image does not preserve Chat image detail preference",
				})
			}
		default:
			return nil, nil, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedContent,
				fmt.Sprintf("Chat content type %q cannot be represented by Responses", part.Type),
			)
		}
	}
	return parts, losses, nil
}

func chatResponsesTools(source []chat.Tool) ([]responses.Tool, error) {
	if len(source) == 0 {
		return nil, nil
	}
	target := make([]responses.Tool, 0, len(source))
	for _, tool := range source {
		if tool.Type != "function" || tool.Function.Name == "" {
			return nil, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedTool,
				fmt.Sprintf("Chat tool type %q cannot be represented by Responses", tool.Type),
			)
		}
		target = append(target, responses.Tool{
			Type:        "function",
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	return target, nil
}

func chatResponsesChatTools(source []responses.Tool) ([]chat.Tool, error) {
	if len(source) == 0 {
		return nil, nil
	}
	target := make([]chat.Tool, 0, len(source))
	for _, tool := range source {
		if tool.Type != "function" || tool.Name == "" {
			return nil, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedTool,
				fmt.Sprintf("Responses tool type %q cannot be represented by Chat Completions", tool.Type),
			)
		}
		target = append(target, chat.Tool{
			Type: "function",
			Function: chat.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}
	return target, nil
}

func chatResponsesToolChoice(source json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(source)) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(source, &value); err != nil {
		return nil, chatResponsesInvalidRequest("decode Chat tool_choice", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return append(json.RawMessage(nil), source...), nil
	}
	typeName, _ := object["type"].(string)
	function, _ := object["function"].(map[string]any)
	name, _ := function["name"].(string)
	if typeName != "function" || name == "" {
		return nil, chatResponsesUnsupported(chatResponsesCodeUnsupportedTool, "unsupported Chat tool_choice")
	}
	return json.Marshal(map[string]string{"type": "function", "name": name})
}

func chatResponsesChatToolChoice(source json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(source)) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(source, &value); err != nil {
		return nil, chatResponsesInvalidRequest("decode Responses tool_choice", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return append(json.RawMessage(nil), source...), nil
	}
	typeName, _ := object["type"].(string)
	name, _ := object["name"].(string)
	if typeName != "function" || name == "" {
		return nil, chatResponsesUnsupported(chatResponsesCodeUnsupportedTool, "unsupported Responses tool_choice")
	}
	return json.Marshal(map[string]any{
		"type":     "function",
		"function": map[string]string{"name": name},
	})
}

func chatResponsesInputMessage(item responses.InputItem) (chat.Message, error) {
	switch item.Role {
	case "system", "developer", "user", "assistant":
	default:
		return chat.Message{}, chatResponsesUnsupported(
			chatResponsesCodeUnsupportedMessageRole,
			fmt.Sprintf("Responses message role %q cannot be represented by Chat Completions", item.Role),
		)
	}

	parts := make([]chat.ContentPart, 0, len(item.Content))
	textOnly := len(item.Content) == 1
	for _, content := range item.Content {
		switch content.Type {
		case "input_text", "output_text":
			parts = append(parts, chat.ContentPart{Type: "text", Text: content.Text})
		case "input_image":
			if content.ImageURL == "" {
				return chat.Message{}, chatResponsesUnsupported(
					chatResponsesCodeUnsupportedContent,
					"Responses input_image without image_url cannot be represented by Chat Completions",
				)
			}
			textOnly = false
			parts = append(parts, chat.ContentPart{
				Type:     "image_url",
				ImageURL: &chat.ImageURL{URL: content.ImageURL},
			})
		default:
			return chat.Message{}, chatResponsesUnsupported(
				chatResponsesCodeUnsupportedContent,
				fmt.Sprintf("Responses content type %q cannot be represented by Chat Completions", content.Type),
			)
		}
	}

	message := chat.Message{Role: item.Role}
	if len(parts) == 0 {
		return message, nil
	}
	if textOnly && parts[0].Type == "text" {
		message.Content = chat.TextContent(parts[0].Text)
		return message, nil
	}
	message.Content = chat.PartsContent(parts)
	return message, nil
}

func chatResponsesAppendToolCall(messages *[]chat.Message, call chat.ToolCall) {
	if len(*messages) > 0 {
		last := &(*messages)[len(*messages)-1]
		if last.Role == "assistant" {
			last.ToolCalls = append(last.ToolCalls, call)
			return
		}
	}
	*messages = append(*messages, chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{call}})
}

func chatResponsesInvalidRequest(operation string, err error) error {
	return &model.ProxyError{
		Kind:    model.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    chatResponsesCodeInvalidRequest,
		Message: operation + ": " + err.Error(),
		Cause:   err,
	}
}

func chatResponsesUnsupported(code, message string) error {
	return &model.ProxyError{
		Kind:    model.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    code,
		Message: message,
	}
}
