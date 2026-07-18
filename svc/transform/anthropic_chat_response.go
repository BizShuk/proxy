package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/chat"
)

// AnthropicToChatResponse converts an Anthropic response to Chat.
func AnthropicToChatResponse(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := anthropic.DecodeResponse(envelope.Body)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	message := chat.Message{Role: "assistant"}
	var text string
	for _, block := range source.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "thinking":
			message.ReasoningContent += block.Thinking
		case "tool_use":
			if len(block.Input) > 0 && !json.Valid(block.Input) {
				return model.TransformResult{}, protocolFailure(fmt.Errorf("anthropic tool input is not valid JSON"))
			}
			arguments := string(block.Input)
			if arguments == "" {
				arguments = "{}"
			}
			message.ToolCalls = append(message.ToolCalls, chat.ToolCall{
				ID: block.ID, Type: "function",
				Function: chat.FunctionCall{Name: block.Name, Arguments: arguments},
			})
		default:
			return model.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Anthropic response block %q", block.Type))
		}
	}
	if text != "" {
		message.Content = chat.TextContent(text)
	}
	cached := source.Usage.CacheCreationInputTokens + source.Usage.CacheReadInputTokens
	response := chat.Response{
		ID: source.ID, Object: "chat.completion", Created: time.Now().Unix(),
		Model:   responseModel(envelope, source.Model),
		Choices: []chat.Choice{{Index: 0, Message: message, FinishReason: anthropicToChatStop(source.StopReason)}},
		Usage: chat.Usage{
			PromptTokens: source.Usage.InputTokens + cached, CompletionTokens: source.Usage.OutputTokens,
			TotalTokens:         source.Usage.InputTokens + cached + source.Usage.OutputTokens,
			PromptTokensDetails: &chat.UsageDetails{CachedTokens: cached},
		},
	}
	body, err := chat.Encode(response)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: body, Losses: cachedTokenLoss(source.Usage.CacheCreationInputTokens)}, nil
}

// ChatToAnthropicResponse converts a Chat response to Anthropic.
func ChatToAnthropicResponse(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
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
	content := make(anthropic.ContentList, 0, 2+len(choice.Message.ToolCalls))
	if choice.Message.ReasoningContent != "" {
		content = append(content, anthropic.Content{Type: "thinking", Thinking: choice.Message.ReasoningContent})
	}
	if choice.Message.Content != nil {
		if choice.Message.Content.Parts != nil {
			for _, part := range choice.Message.Content.Parts {
				if part.Type != "text" {
					return model.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Chat response content part %q", part.Type))
				}
				content = append(content, anthropic.Content{Type: "text", Text: part.Text})
			}
		} else if choice.Message.Content.Text != "" {
			content = append(content, anthropic.Content{Type: "text", Text: choice.Message.Content.Text})
		}
	}
	for _, toolCall := range choice.Message.ToolCalls {
		arguments, parseErr := validateArguments(toolCall.Function.Arguments)
		if parseErr != nil {
			return model.TransformResult{}, parseErr
		}
		content = append(content, anthropic.Content{
			Type: "tool_use", ID: toolCall.ID, Name: toolCall.Function.Name, Input: arguments,
		})
	}
	cached := 0
	if source.Usage.PromptTokensDetails != nil {
		cached = source.Usage.PromptTokensDetails.CachedTokens
	}
	if cached > source.Usage.PromptTokens {
		return model.TransformResult{}, protocolFailure(fmt.Errorf("cached prompt tokens exceed total prompt tokens"))
	}
	response := anthropic.Response{
		ID: source.ID, Type: "message", Role: "assistant", Content: content,
		Model: responseModel(envelope, source.Model), StopReason: chatToAnthropicStop(choice.FinishReason),
		Usage: anthropic.Usage{
			InputTokens: source.Usage.PromptTokens - cached, CacheReadInputTokens: cached,
			OutputTokens: source.Usage.CompletionTokens,
		},
	}
	body, err := anthropic.Encode(response)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: body}, nil
}
