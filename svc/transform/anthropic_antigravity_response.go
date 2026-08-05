package transform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/antigravity"
)

// AntigravityToAnthropicResponse converts one non-streaming Antigravity
// response envelope back to an Anthropic message.
func AntigravityToAnthropicResponse(ctx context.Context, envelope model.ResponseEnvelope) (model.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return model.TransformResult{}, err
	}
	source, err := antigravity.DecodeResponse(envelope.Body)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	body := source
	if len(body.Candidates) == 0 {
		return model.TransformResult{}, protocolFailure(fmt.Errorf("antigravity response has no candidates"))
	}
	candidate := body.Candidates[0]

	content, hasToolUse, err := antigravityPartsToAnthropic(candidate.Content.Parts, envelope.Exchange)
	if err != nil {
		return model.TransformResult{}, err
	}

	stopReason := antigravity.StopReason(candidate.FinishReason)
	if hasToolUse {
		stopReason = "tool_use"
	}

	usage := antigravityUsage(body.UsageMetadata, body.CachedTokenCount)
	responseID := body.ResponseID
	if responseID == "" {
		responseID = generatedID(envelope.Exchange, "msg_antigravity")
	}

	response := anthropic.Response{
		ID:         responseID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      responseModel(envelope, body.ModelVersion),
		StopReason: stopReason,
		Usage:      usage,
	}
	encoded, err := anthropic.Encode(response)
	if err != nil {
		return model.TransformResult{}, protocolFailure(err)
	}
	return model.TransformResult{Body: encoded}, nil
}

// antigravityPartsToAnthropic maps one candidate's parts onto Anthropic content
// blocks, reporting whether the turn issued a tool call.
func antigravityPartsToAnthropic(
	parts []antigravity.Part,
	exchange model.Exchange,
) (anthropic.ContentList, bool, error) {
	content := make(anthropic.ContentList, 0, len(parts))
	hasToolUse := false

	for index, part := range parts {
		switch {
		case part.FunctionCall != nil:
			args, err := antigravityCallArgs(part.FunctionCall.Args)
			if err != nil {
				return nil, false, protocolFailure(fmt.Errorf("candidate part %d: %w", index, err))
			}
			callID := part.FunctionCall.ID
			if callID == "" {
				callID = generatedID(exchange, fmt.Sprintf("toolu_%s_%d", part.FunctionCall.Name, index))
			}
			// Gemini rejects a replayed functionCall without its original
			// thought signature, so it has to survive the round-trip through
			// the client — on the block for clients that keep unknown fields,
			// in the cache for the ones that do not.
			antigravityToolSignatures.Remember(callID, part.ThoughtSignature)
			content = append(content, anthropic.Content{
				Type:      "tool_use",
				ID:        callID,
				Name:      part.FunctionCall.Name,
				Input:     args,
				Signature: part.ThoughtSignature,
			})
			hasToolUse = true
		case part.Thought:
			if part.Text == "" && part.ThoughtSignature == "" {
				continue
			}
			content = append(content, anthropic.Content{
				Type:      "thinking",
				Thinking:  part.Text,
				Signature: part.ThoughtSignature,
			})
		case part.Text != "":
			content = append(content, anthropic.Content{Type: "text", Text: part.Text})
		case part.InlineData != nil:
			return nil, false, protocolFailure(fmt.Errorf(
				"candidate part %d returns inline media, which Anthropic Messages cannot carry in an assistant turn", index))
		}
	}
	return content, hasToolUse, nil
}

// antigravityCallArgs renders a tool call's arguments as the JSON object
// Anthropic carries in tool_use.input.
func antigravityCallArgs(args map[string]any) (json.RawMessage, error) {
	if len(args) == 0 {
		return json.RawMessage("{}"), nil
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("function call args are not encodable: %w", err)
	}
	return encoded, nil
}

// antigravityUsage folds Gemini token accounting into Anthropic's buckets.
// promptTokenCount includes cached tokens, which Anthropic reports separately.
func antigravityUsage(usage *antigravity.UsageMetadata, cached int) anthropic.Usage {
	if usage == nil {
		return anthropic.Usage{}
	}
	input := usage.PromptTokenCount - cached
	if input < 0 {
		input = 0
	}
	output := usage.CandidatesTokenCount + usage.ThoughtsTokenCount
	if output == 0 && usage.TotalTokenCount > 0 {
		output = usage.TotalTokenCount - usage.PromptTokenCount
		if output < 0 {
			output = 0
		}
	}
	return anthropic.Usage{
		InputTokens:          input,
		OutputTokens:         output,
		CacheReadInputTokens: cached,
	}
}
