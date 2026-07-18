package transform

import (
	"context"
	"fmt"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/chat"
	"github.com/bizshuk/proxy/model/responses"
)

// ChatToResponsesResponse converts a Chat response to Responses.
func ChatToResponsesResponse(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := chat.DecodeResponse(envelope.Body)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	if len(source.Choices) == 0 {
		return model.TransformResult{}, protocolFailure(fmt.Errorf("chat response has no choices"))
	}
	choice := source.Choices[0]
	output := make([]responses.OutputItem, 0, 2+len(choice.Message.ToolCalls))
	if choice.Message.ReasoningContent != "" {
		output = append(output, responses.OutputItem{
			ID: generatedID(envelope.Exchange, source.ID+"_reasoning"), Type: "reasoning",
			Summary: responses.ContentList{{Type: "summary_text", Text: choice.Message.ReasoningContent}},
		})
	}
	if choice.Message.Content != nil {
		parts := responses.ContentList{}
		if choice.Message.Content.Parts != nil {
			for _, part := range choice.Message.Content.Parts {
				if part.Type != "text" {
					return model.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Chat response content part %q", part.Type))
				}
				parts = append(parts, responses.Content{Type: "output_text", Text: part.Text})
			}
		} else if choice.Message.Content.Text != "" {
			parts = append(parts, responses.Content{Type: "output_text", Text: choice.Message.Content.Text})
		}
		if len(parts) > 0 {
			output = append(output, responses.OutputItem{
				ID: generatedID(envelope.Exchange, source.ID+"_message"), Type: "message",
				Role: "assistant", Status: "completed", Content: parts,
			})
		}
	}
	for index, toolCall := range choice.Message.ToolCalls {
		if _, parseErr := validateArguments(toolCall.Function.Arguments); parseErr != nil {
			return model.TransformResult{}, parseErr
		}
		output = append(output, responses.OutputItem{
			ID:   generatedID(envelope.Exchange, fmt.Sprintf("%s_function_%d", source.ID, index)),
			Type: "function_call", CallID: toolCall.ID,
			Name: toolCall.Function.Name, Arguments: toolCall.Function.Arguments,
		})
	}
	cached := 0
	if source.Usage.PromptTokensDetails != nil {
		cached = source.Usage.PromptTokensDetails.CachedTokens
	}
	response := responses.Response{
		ID: source.ID, Object: "response", Model: responseModel(envelope, source.Model),
		Output: output, Status: "completed",
		Usage: &responses.Usage{
			InputTokens: source.Usage.PromptTokens, OutputTokens: source.Usage.CompletionTokens,
			TotalTokens:        source.Usage.TotalTokens,
			InputTokensDetails: &responses.InputTokensDetails{CachedTokens: cached},
		},
	}
	if choice.FinishReason == "length" {
		response.Status = "incomplete"
		response.IncompleteDetails = &responses.IncompleteDetails{Reason: "max_output_tokens"}
	}
	body, err := responses.Encode(response)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: body}, nil
}

// ResponsesToChatResponse converts a Responses result to Chat.
func ResponsesToChatResponse(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := responses.DecodeResponse(envelope.Body)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	message := chat.Message{Role: "assistant"}
	var text string
	hasTool := false
	for _, item := range source.Output {
		switch item.Type {
		case "reasoning":
			for _, summary := range item.Summary {
				message.ReasoningContent += summary.Text
			}
		case "message":
			for _, part := range item.Content {
				if part.Type != "output_text" {
					return model.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Responses output content %q", part.Type))
				}
				text += part.Text
			}
		case "function_call":
			if _, parseErr := validateArguments(item.Arguments); parseErr != nil {
				return model.TransformResult{}, parseErr
			}
			message.ToolCalls = append(message.ToolCalls, chat.ToolCall{
				ID: item.CallID, Type: "function",
				Function: chat.FunctionCall{Name: item.Name, Arguments: item.Arguments},
			})
			hasTool = true
		default:
			return model.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Responses output item %q", item.Type))
		}
	}
	if text != "" {
		message.Content = chat.TextContent(text)
	}
	finishReason := "stop"
	if hasTool {
		finishReason = "tool_calls"
	} else if source.Status == "incomplete" {
		finishReason = "length"
	}
	usage := chat.Usage{}
	if source.Usage != nil {
		usage.PromptTokens = source.Usage.InputTokens
		usage.CompletionTokens = source.Usage.OutputTokens
		usage.TotalTokens = source.Usage.TotalTokens
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		if source.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails = &chat.UsageDetails{CachedTokens: source.Usage.InputTokensDetails.CachedTokens}
		}
	}
	response := chat.Response{
		ID: source.ID, Object: "chat.completion", Created: time.Now().Unix(),
		Model:   responseModel(envelope, source.Model),
		Choices: []chat.Choice{{Index: 0, Message: message, FinishReason: finishReason}}, Usage: usage,
	}
	body, err := chat.Encode(response)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: body}, nil
}
