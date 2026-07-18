package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/responses"
)

// AnthropicToResponsesRequest converts an Anthropic Messages request to an
// OpenAI Responses request without applying provider-specific defaults.
func AnthropicToResponsesRequest(ctx context.Context, env model.RequestEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}

	src, err := anthropic.DecodeRequest(env.Body)
	if err != nil {
		return model.TransformResult{}, task5InvalidRequest("decode anthropic request", err)
	}

	input, messageLosses, err := task5AnthropicInput(src.Messages)
	if err != nil {
		return model.TransformResult{}, err
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode responses input: %w", err)
	}

	instructions, systemLosses, err := task5AnthropicInstructions(src.System)
	if err != nil {
		return model.TransformResult{}, err
	}
	tools, err := task5AnthropicTools(src.Tools)
	if err != nil {
		return model.TransformResult{}, err
	}
	toolChoice, err := task5AnthropicToolChoice(src.ToolChoice)
	if err != nil {
		return model.TransformResult{}, err
	}

	stream := env.Stream
	dst := responses.Request{
		Model:        env.Model,
		Input:        inputJSON,
		Instructions: instructions,
		Stream:       &stream,
		Tools:        tools,
		ToolChoice:   toolChoice,
		Reasoning:    task5ResponsesReasoning(src.Thinking),
	}
	if src.MaxTokens != 0 {
		maxTokens := src.MaxTokens
		dst.MaxOutputTokens = &maxTokens
	}
	body, err := responses.Encode(dst)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode responses request: %w", err)
	}

	losses := append(messageLosses, systemLosses...)
	losses = append(losses, task5AnthropicRequestLosses(src)...)
	return model.TransformResult{Body: body, Losses: losses}, nil
}

// ResponsesToAnthropicRequest converts an OpenAI Responses request to an
// Anthropic Messages request and rejects provider-side state and tools.
func ResponsesToAnthropicRequest(ctx context.Context, env model.RequestEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}

	src, err := responses.DecodeRequest(env.Body)
	if err != nil {
		return model.TransformResult{}, task5InvalidRequest("decode responses request", err)
	}
	if src.PreviousResponseID != "" {
		return model.TransformResult{}, task5Unsupported(
			"stateful_context_not_portable",
			"previous_response_id cannot be translated without provider-side history",
		)
	}

	tools, err := task5ResponsesTools(src.Tools)
	if err != nil {
		return model.TransformResult{}, err
	}
	toolChoice, err := task5ResponsesToolChoice(src.ToolChoice)
	if err != nil {
		return model.TransformResult{}, err
	}
	items, err := responses.DecodeInput(src.Input)
	if err != nil {
		return model.TransformResult{}, task5InvalidRequest("decode responses input", err)
	}
	messages, system, losses, err := task5ResponsesInput(items)
	if err != nil {
		return model.TransformResult{}, err
	}
	if src.Instructions != "" {
		system = append(anthropic.ContentList{{Type: "text", Text: src.Instructions}}, system...)
	}

	thinking, reasoningLoss, err := task5AnthropicThinking(src.Reasoning)
	if err != nil {
		return model.TransformResult{}, err
	}
	losses = append(losses, reasoningLoss...)
	dst := anthropic.Request{
		Model:      env.Model,
		Messages:   messages,
		System:     system,
		Stream:     env.Stream,
		Tools:      tools,
		ToolChoice: toolChoice,
		Thinking:   thinking,
	}
	if src.MaxOutputTokens != nil {
		dst.MaxTokens = *src.MaxOutputTokens
	}
	body, err := anthropic.Encode(dst)
	if err != nil {
		return model.TransformResult{}, fmt.Errorf("encode anthropic request: %w", err)
	}
	return model.TransformResult{Body: body, Losses: losses}, nil
}

func task5AnthropicInstructions(system anthropic.ContentList) (string, []model.SemanticLoss, error) {
	parts := make([]string, 0, len(system))
	var losses []model.SemanticLoss
	for _, block := range system {
		if block.Type != "text" {
			return "", nil, task5Unsupported("unsupported_content", "Responses instructions only support Anthropic text system blocks")
		}
		parts = append(parts, block.Text)
		if len(block.CacheControl) > 0 {
			losses = append(losses, model.SemanticLoss{
				Field: "system.cache_control", Reason: "Responses instructions do not preserve Anthropic cache control",
			})
		}
	}
	return strings.Join(parts, "\n"), losses, nil
}

func task5AnthropicInput(messages []anthropic.Message) ([]responses.InputItem, []model.SemanticLoss, error) {
	var items []responses.InputItem
	var losses []model.SemanticLoss
	for _, message := range messages {
		switch message.Role {
		case "user", "assistant", "system", "developer":
		default:
			return nil, nil, task5Unsupported(
				"unsupported_role",
				fmt.Sprintf("Responses cannot reproduce Anthropic message role %q", message.Role),
			)
		}
		var content responses.ContentList
		flush := func() {
			if len(content) == 0 {
				return
			}
			items = append(items, responses.InputItem{Type: "message", Role: message.Role, Content: content})
			content = nil
		}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				contentType := "input_text"
				if message.Role == "assistant" {
					contentType = "output_text"
				}
				content = append(content, responses.Content{Type: contentType, Text: block.Text})
			case "image":
				if message.Role != "user" {
					return nil, nil, task5Unsupported("unsupported_content", "Anthropic assistant images cannot be represented in Responses input")
				}
				imageURL, err := task5AnthropicImageURL(block.Source)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, responses.Content{Type: "input_image", ImageURL: imageURL})
			case "tool_use":
				flush()
				arguments, err := task5CompactJSON(block.Input)
				if err != nil {
					return nil, nil, task5InvalidRequest("decode Anthropic tool arguments", err)
				}
				items = append(items, responses.InputItem{
					Type: "function_call", CallID: block.ID, Name: block.Name, Arguments: arguments,
				})
			case "tool_result":
				flush()
				output, err := task5AnthropicToolOutput(block)
				if err != nil {
					return nil, nil, err
				}
				items = append(items, responses.InputItem{
					Type: "function_call_output", CallID: block.ToolUseID, Output: output,
				})
				if block.IsError {
					losses = append(losses, model.SemanticLoss{
						Field:  "messages.content.tool_result.is_error",
						Reason: "Responses function_call_output does not preserve Anthropic is_error",
					})
				}
			default:
				return nil, nil, task5Unsupported("unsupported_content", "Anthropic content block cannot be represented in Responses input")
			}
			if len(block.CacheControl) > 0 {
				losses = append(losses, model.SemanticLoss{
					Field: "messages.content.cache_control", Reason: "Responses input does not preserve Anthropic cache control",
				})
			}
		}
		flush()
	}
	return items, losses, nil
}

func task5CompactJSON(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", err
	}
	return compact.String(), nil
}

func task5AnthropicImageURL(source *anthropic.Source) (string, error) {
	if source == nil || source.Type != "base64" || source.MediaType == "" {
		return "", task5Unsupported("unsupported_image", "Responses input requires an Anthropic base64 image source with a media type")
	}
	return "data:" + source.MediaType + ";base64," + source.Data, nil
}

func task5AnthropicToolOutput(block anthropic.Content) (string, error) {
	if len(block.Content) == 0 {
		return block.Text, nil
	}
	var text string
	if err := json.Unmarshal(block.Content, &text); err == nil {
		return text, nil
	}
	var blocks anthropic.ContentList
	if err := json.Unmarshal(block.Content, &blocks); err != nil {
		return "", task5InvalidRequest("decode Anthropic tool result", err)
	}
	var output strings.Builder
	for _, part := range blocks {
		if part.Type != "text" {
			return "", task5Unsupported("unsupported_tool_result", "Responses function output only supports Anthropic text tool results")
		}
		output.WriteString(part.Text)
	}
	return output.String(), nil
}

func task5AnthropicTools(tools []anthropic.Tool) ([]responses.Tool, error) {
	result := make([]responses.Tool, 0, len(tools))
	for _, tool := range tools {
		parameters := tool.InputSchema
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		result = append(result, responses.Tool{
			Type: "function", Name: tool.Name, Description: tool.Description, Parameters: parameters,
		})
	}
	return result, nil
}

func task5AnthropicToolChoice(choice *anthropic.ToolChoice) (json.RawMessage, error) {
	if choice == nil {
		return nil, nil
	}
	var value any
	switch choice.Type {
	case "auto", "none":
		value = choice.Type
	case "any":
		value = "required"
	case "tool":
		value = struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}{Type: "function", Name: choice.Name}
	default:
		return nil, task5Unsupported("unsupported_tool_choice", "Anthropic tool choice cannot be represented in Responses")
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode responses tool choice: %w", err)
	}
	return body, nil
}

func task5ResponsesReasoning(thinking *anthropic.Thinking) *responses.Reasoning {
	if thinking == nil || thinking.BudgetTokens <= 0 || thinking.Type == "disabled" {
		return nil
	}
	effort := "high"
	if thinking.BudgetTokens <= 1024 {
		effort = "low"
	} else if thinking.BudgetTokens < 4096 {
		effort = "medium"
	}
	return &responses.Reasoning{Effort: effort}
}

func task5AnthropicRequestLosses(request *anthropic.Request) []model.SemanticLoss {
	var losses []model.SemanticLoss
	if request.Thinking != nil && request.Thinking.BudgetTokens > 0 && request.Thinking.Type != "disabled" {
		losses = append(losses, model.SemanticLoss{
			Field:  "thinking.budget_tokens",
			Reason: "Responses reasoning effort preserves only a low/medium/high bucket",
		})
	}
	if request.Temperature != nil {
		losses = append(losses, model.SemanticLoss{Field: "temperature", Reason: "Responses request DTO does not expose temperature"})
	}
	if request.TopP != nil {
		losses = append(losses, model.SemanticLoss{Field: "top_p", Reason: "Responses request DTO does not expose top_p"})
	}
	if len(request.StopSequences) > 0 {
		losses = append(losses, model.SemanticLoss{Field: "stop_sequences", Reason: "Responses request DTO does not expose stop sequences"})
	}
	if len(request.Metadata) > 0 {
		losses = append(losses, model.SemanticLoss{Field: "metadata", Reason: "Responses request DTO does not expose Anthropic metadata"})
	}
	return losses
}

func task5ResponsesInput(items []responses.InputItem) ([]anthropic.Message, anthropic.ContentList, []model.SemanticLoss, error) {
	var messages []anthropic.Message
	var system anthropic.ContentList
	var losses []model.SemanticLoss
	for _, item := range items {
		switch item.Type {
		case "message":
			blocks, err := task5ResponsesContent(item.Role, item.Content)
			if err != nil {
				return nil, nil, nil, err
			}
			switch item.Role {
			case "user", "assistant":
				messages = task5AppendAnthropicMessage(messages, item.Role, blocks)
			case "system", "developer":
				system = append(system, blocks...)
				losses = append(losses, model.SemanticLoss{
					Field: "input.role", Reason: "Anthropic system blocks do not preserve Responses input message priority",
				})
			default:
				return nil, nil, nil, task5Unsupported("unsupported_role", "Anthropic Messages cannot represent the Responses input role")
			}
		case "function_call":
			arguments := json.RawMessage(item.Arguments)
			if len(arguments) == 0 {
				arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(arguments) {
				return nil, nil, nil, task5InvalidRequest("decode Responses function arguments", fmt.Errorf("arguments must be valid JSON"))
			}
			messages = task5AppendAnthropicMessage(messages, "assistant", anthropic.ContentList{{
				Type: "tool_use", ID: item.CallID, Name: item.Name, Input: arguments,
			}})
		case "function_call_output":
			output, err := json.Marshal(item.Output)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("encode Anthropic tool result: %w", err)
			}
			messages = task5AppendAnthropicMessage(messages, "user", anthropic.ContentList{{
				Type: "tool_result", ToolUseID: item.CallID, Content: output,
			}})
		default:
			return nil, nil, nil, task5Unsupported("unsupported_input", "Anthropic Messages cannot represent the Responses input item")
		}
	}
	return messages, system, losses, nil
}

func task5ResponsesContent(role string, content responses.ContentList) (anthropic.ContentList, error) {
	if len(content) == 0 {
		return nil, task5InvalidRequest("decode Responses message", fmt.Errorf("content is required"))
	}
	blocks := make(anthropic.ContentList, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "input_text", "output_text":
			blocks = append(blocks, anthropic.Content{Type: "text", Text: part.Text})
		case "input_image":
			if role != "user" {
				return nil, task5Unsupported("unsupported_content", "Anthropic Messages only accepts input images in user messages")
			}
			source, err := task5ResponsesImageSource(part.ImageURL)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, anthropic.Content{Type: "image", Source: source})
		default:
			return nil, task5Unsupported("unsupported_content", "Anthropic Messages cannot represent the Responses content part")
		}
	}
	return blocks, nil
}

func task5ResponsesImageSource(imageURL string) (*anthropic.Source, error) {
	const BASE64_MARKER = ";base64,"
	if !strings.HasPrefix(imageURL, "data:") {
		return nil, task5Unsupported("unsupported_image", "Anthropic Messages requires a base64 data URL")
	}
	mediaAndData := strings.TrimPrefix(imageURL, "data:")
	index := strings.Index(mediaAndData, BASE64_MARKER)
	if index <= 0 {
		return nil, task5Unsupported("unsupported_image", "Anthropic Messages requires a base64 data URL with a media type")
	}
	return &anthropic.Source{
		Type: "base64", MediaType: mediaAndData[:index], Data: mediaAndData[index+len(BASE64_MARKER):],
	}, nil
}

func task5AppendAnthropicMessage(messages []anthropic.Message, role string, content anthropic.ContentList) []anthropic.Message {
	if len(messages) > 0 && messages[len(messages)-1].Role == role {
		messages[len(messages)-1].Content = append(messages[len(messages)-1].Content, content...)
		return messages
	}
	return append(messages, anthropic.Message{Role: role, Content: content})
}

func task5ResponsesTools(tools []responses.Tool) ([]anthropic.Tool, error) {
	result := make([]anthropic.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			return nil, task5Unsupported("unsupported_tool", "Anthropic Messages cannot reproduce provider-side built-in tools")
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		result = append(result, anthropic.Tool{
			Name: tool.Name, Description: tool.Description, InputSchema: parameters,
		})
	}
	return result, nil
}

func task5ResponsesToolChoice(raw json.RawMessage) (*anthropic.ToolChoice, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		switch name {
		case "auto", "none":
			return &anthropic.ToolChoice{Type: name}, nil
		case "required":
			return &anthropic.ToolChoice{Type: "any"}, nil
		default:
			return nil, task5Unsupported("unsupported_tool_choice", "Responses tool choice cannot be represented in Anthropic Messages")
		}
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil, task5InvalidRequest("decode Responses tool choice", err)
	}
	if choice.Type != "function" || choice.Name == "" {
		return nil, task5Unsupported("unsupported_tool_choice", "Responses tool choice cannot be represented in Anthropic Messages")
	}
	return &anthropic.ToolChoice{Type: "tool", Name: choice.Name}, nil
}

func task5AnthropicThinking(reasoning *responses.Reasoning) (*anthropic.Thinking, []model.SemanticLoss, error) {
	if reasoning == nil || reasoning.Effort == "" {
		return nil, nil, nil
	}
	budgets := map[string]int{"low": 1024, "medium": 2048, "high": 4096}
	budget, ok := budgets[reasoning.Effort]
	if !ok {
		return nil, nil, task5Unsupported("unsupported_reasoning_effort", "Anthropic thinking cannot represent the Responses reasoning effort")
	}
	return &anthropic.Thinking{Type: "enabled", BudgetTokens: budget}, []model.SemanticLoss{{
		Field: "reasoning.effort", Reason: "Anthropic thinking uses an approximate token budget for the Responses effort bucket",
	}}, nil
}

func task5InvalidRequest(operation string, err error) error {
	return &model.ProxyError{
		Kind: model.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
		Code: "invalid_request", Message: operation + ": " + err.Error(), Cause: err,
	}
}

func task5Unsupported(code, message string) error {
	return &model.ProxyError{
		Kind: model.ERROR_UNSUPPORTED_FEATURE, Status: http.StatusBadRequest,
		Code: code, Message: message,
	}
}
