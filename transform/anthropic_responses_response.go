package transform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/anthropic"
	"github.com/bizshuk/proxy/protocol/responses"
)

// AnthropicToResponsesResponse converts an Anthropic response to Responses.
func AnthropicToResponsesResponse(ctx context.Context, envelope protocol.ResponseEnvelope) (protocol.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TransformResult{}, err
	}
	source, err := anthropic.DecodeResponse(envelope.Body)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	output := make([]responses.OutputItem, 0, len(source.Content))
	for index, block := range source.Content {
		switch block.Type {
		case "thinking":
			output = append(output, responses.OutputItem{
				ID:   generatedID(envelope.Exchange, fmt.Sprintf("%s_reasoning_%d", source.ID, index)),
				Type: "reasoning", Summary: responses.ContentList{{Type: "summary_text", Text: block.Thinking}},
			})
		case "text":
			output = append(output, responses.OutputItem{
				ID:   generatedID(envelope.Exchange, fmt.Sprintf("%s_message_%d", source.ID, index)),
				Type: "message", Role: "assistant", Status: "completed",
				Content: responses.ContentList{{Type: "output_text", Text: block.Text}},
			})
		case "tool_use":
			if len(block.Input) > 0 && !json.Valid(block.Input) {
				return protocol.TransformResult{}, protocolFailure(fmt.Errorf("anthropic tool input is not valid JSON"))
			}
			arguments := string(block.Input)
			if arguments == "" {
				arguments = "{}"
			}
			output = append(output, responses.OutputItem{
				ID:   generatedID(envelope.Exchange, fmt.Sprintf("%s_function_%d", source.ID, index)),
				Type: "function_call", CallID: block.ID, Name: block.Name, Arguments: arguments,
			})
		default:
			return protocol.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Anthropic response block %q", block.Type))
		}
	}
	cached := source.Usage.CacheCreationInputTokens + source.Usage.CacheReadInputTokens
	response := responses.Response{
		ID: source.ID, Object: "response", Model: responseModel(envelope, source.Model),
		Output: output, Status: responsesStatusFromAnthropic(source.StopReason),
		Usage: &responses.Usage{
			InputTokens: source.Usage.InputTokens + cached, OutputTokens: source.Usage.OutputTokens,
			TotalTokens:        source.Usage.InputTokens + cached + source.Usage.OutputTokens,
			InputTokensDetails: &responses.InputTokensDetails{CachedTokens: cached},
		},
	}
	if source.StopReason == "max_tokens" {
		response.IncompleteDetails = &responses.IncompleteDetails{Reason: "max_output_tokens"}
	}
	body, err := responses.Encode(response)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	return protocol.TransformResult{Body: body, Losses: cachedTokenLoss(source.Usage.CacheCreationInputTokens)}, nil
}

// ResponsesToAnthropicResponse converts a Responses result to Anthropic.
func ResponsesToAnthropicResponse(ctx context.Context, envelope protocol.ResponseEnvelope) (protocol.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TransformResult{}, err
	}
	source, err := responses.DecodeResponse(envelope.Body)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	content := make(anthropic.ContentList, 0, len(source.Output))
	hasTool := false
	for _, item := range source.Output {
		switch item.Type {
		case "reasoning":
			for _, summary := range item.Summary {
				content = append(content, anthropic.Content{Type: "thinking", Thinking: summary.Text})
			}
		case "message":
			for _, part := range item.Content {
				if part.Type != "output_text" {
					return protocol.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Responses output content %q", part.Type))
				}
				content = append(content, anthropic.Content{Type: "text", Text: part.Text})
			}
		case "function_call":
			arguments, parseErr := validateArguments(item.Arguments)
			if parseErr != nil {
				return protocol.TransformResult{}, parseErr
			}
			content = append(content, anthropic.Content{Type: "tool_use", ID: item.CallID, Name: item.Name, Input: arguments})
			hasTool = true
		default:
			return protocol.TransformResult{}, protocolFailure(fmt.Errorf("unsupported Responses output item %q", item.Type))
		}
	}
	usage, err := responsesToAnthropicUsage(source.Usage)
	if err != nil {
		return protocol.TransformResult{}, err
	}
	stop := "end_turn"
	if hasTool {
		stop = "tool_use"
	} else if source.Status == "incomplete" {
		stop = "max_tokens"
	}
	response := anthropic.Response{
		ID: source.ID, Type: "message", Role: "assistant", Content: content,
		Model: responseModel(envelope, source.Model), StopReason: stop, Usage: usage,
	}
	body, err := anthropic.Encode(response)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	return protocol.TransformResult{Body: body}, nil
}

func responsesStatusFromAnthropic(stop string) string {
	if stop == "max_tokens" {
		return "incomplete"
	}
	return "completed"
}

func responsesToAnthropicUsage(usage *responses.Usage) (anthropic.Usage, error) {
	if usage == nil {
		return anthropic.Usage{}, nil
	}
	cached := 0
	if usage.InputTokensDetails != nil {
		cached = usage.InputTokensDetails.CachedTokens
	}
	if cached > usage.InputTokens {
		return anthropic.Usage{}, protocolFailure(fmt.Errorf("cached input tokens exceed total input tokens"))
	}
	return anthropic.Usage{
		InputTokens: usage.InputTokens - cached, CacheReadInputTokens: cached,
		OutputTokens: usage.OutputTokens,
	}, nil
}
