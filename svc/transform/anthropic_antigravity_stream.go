package transform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/antigravity"
)

// block kinds the Antigravity stream can hold open on the Anthropic side.
const (
	antigravityBlockNone     = ""
	antigravityBlockText     = "text"
	antigravityBlockThinking = "thinking"
)

type antigravityToAnthropicStream struct {
	exchange   model.Exchange
	messageID  string
	started    bool
	terminal   bool
	sentFinal  bool
	hasContent bool
	hasToolUse bool

	nextBlock int
	openKind  string
	openIndex int

	hasFinish    bool
	finishReason string
	usage        *antigravity.UsageMetadata
	cachedTokens int
	inputTokens  int
}

// NewAntigravityToAnthropicStream creates one isolated Antigravity-to-Anthropic
// stream transform.
func NewAntigravityToAnthropicStream(exchange model.Exchange) (StreamTransform, error) {
	return &antigravityToAnthropicStream{exchange: exchange, openKind: antigravityBlockNone}, nil
}

func (s *antigravityToAnthropicStream) Push(ctx context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Gemini's alt=sse stream ends by closing the body, but some gateway hops
	// append the OpenAI-style sentinel; treat it as a no-op rather than data.
	if string(frame.Data) == "[DONE]" {
		return nil, nil
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("Antigravity frame received after terminal event"))
	}

	var errorEnvelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame.Data, &errorEnvelope); err == nil && len(errorEnvelope.Error) > 0 {
		return s.pushError(errorEnvelope.Error)
	}

	source, err := antigravity.DecodeResponse(frame.Data)
	if err != nil {
		return nil, protocolFailure(fmt.Errorf("decode Antigravity stream frame: %w", err))
	}
	body := source

	output := s.ensureMessageStart(body)
	if len(body.Candidates) > 0 {
		frames, err := s.pushParts(body.Candidates[0].Content.Parts)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
		if reason := body.Candidates[0].FinishReason; reason != "" {
			s.hasFinish = true
			s.finishReason = reason
		}
	}
	if body.UsageMetadata != nil {
		s.usage = body.UsageMetadata
		s.cachedTokens = body.CachedTokenCount
	}
	// Both signals are needed before the terminal delta: the gateway can send
	// the finish reason and the usage block in separate frames.
	if s.hasFinish && s.usage != nil {
		output = append(output, s.finalEvents()...)
	}
	return output, nil
}

func (s *antigravityToAnthropicStream) Close(ctx context.Context) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.started {
		return nil, protocolFailure(fmt.Errorf("Antigravity stream ended before any frame"))
	}
	if s.terminal {
		return nil, nil
	}
	output := s.finalEvents()
	output = append(output, antigravityStreamFrame("message_stop", map[string]any{"type": "message_stop"}))
	s.terminal = true
	return output, nil
}

func (s *antigravityToAnthropicStream) pushError(raw json.RawMessage) ([]model.SSEFrame, error) {
	if !json.Valid(raw) {
		return nil, protocolFailure(fmt.Errorf("Antigravity error frame has invalid error payload"))
	}
	data, err := json.Marshal(struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}{Type: "error", Error: raw})
	if err != nil {
		return nil, protocolFailure(fmt.Errorf("encode Anthropic stream error: %w", err))
	}
	s.terminal = true
	return []model.SSEFrame{{Event: "error", Data: data}}, nil
}

func (s *antigravityToAnthropicStream) ensureMessageStart(body *antigravity.Body) []model.SSEFrame {
	if s.started {
		return nil
	}
	s.started = true
	s.messageID = body.ResponseID
	if s.messageID == "" {
		s.messageID = generatedID(s.exchange, "msg_antigravity_stream")
	}
	if body.UsageMetadata != nil {
		s.inputTokens = body.UsageMetadata.PromptTokenCount - body.CachedTokenCount
		if s.inputTokens < 0 {
			s.inputTokens = 0
		}
	}
	return []model.SSEFrame{antigravityStreamFrame("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         s.exchange.OriginalRequest.Model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         anthropic.Usage{InputTokens: s.inputTokens},
		},
	})}
}

func (s *antigravityToAnthropicStream) pushParts(parts []antigravity.Part) ([]model.SSEFrame, error) {
	var output []model.SSEFrame
	for index, part := range parts {
		switch {
		case part.FunctionCall != nil:
			frames, err := s.pushToolCall(index, part.FunctionCall, part.ThoughtSignature)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		case part.Thought:
			output = append(output, s.pushThinking(part)...)
		case part.Text != "":
			output = append(output, s.pushText(part.Text)...)
		case part.ThoughtSignature != "":
			output = append(output, s.pushSignature(part.ThoughtSignature)...)
		}
	}
	return output, nil
}

func (s *antigravityToAnthropicStream) pushThinking(part antigravity.Part) []model.SSEFrame {
	var output []model.SSEFrame
	if part.Text != "" {
		if s.openKind != antigravityBlockThinking {
			output = append(output, s.closeBlock()...)
			output = append(output, s.openBlock(antigravityBlockThinking, map[string]any{
				"type": "thinking", "thinking": "",
			}))
		}
		output = append(output, antigravityStreamFrame("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": s.openIndex,
			"delta": map[string]any{"type": "thinking_delta", "thinking": part.Text},
		}))
		s.hasContent = true
	}
	if part.ThoughtSignature != "" {
		output = append(output, s.pushSignature(part.ThoughtSignature)...)
	}
	return output
}

// pushSignature attaches a thought signature to the open thinking block.
// Anthropic requires signature_delta inside the thinking block it signs, so a
// signature arriving with no thinking block open has nowhere to go.
func (s *antigravityToAnthropicStream) pushSignature(signature string) []model.SSEFrame {
	if s.openKind != antigravityBlockThinking {
		return nil
	}
	s.hasContent = true
	return []model.SSEFrame{antigravityStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": s.openIndex,
		"delta": map[string]any{"type": "signature_delta", "signature": signature},
	})}
}

func (s *antigravityToAnthropicStream) pushText(text string) []model.SSEFrame {
	var output []model.SSEFrame
	if s.openKind != antigravityBlockText {
		output = append(output, s.closeBlock()...)
		output = append(output, s.openBlock(antigravityBlockText, map[string]any{
			"type": "text", "text": "",
		}))
	}
	output = append(output, antigravityStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": s.openIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	}))
	s.hasContent = true
	return output
}

// pushToolCall emits a complete tool_use block. Gemini delivers the whole
// argument object in one part, so the block opens and closes in one step
// instead of accumulating partial JSON.
func (s *antigravityToAnthropicStream) pushToolCall(index int, call *antigravity.FunctionCall, signature string) ([]model.SSEFrame, error) {
	args, err := antigravityCallArgs(call.Args)
	if err != nil {
		return nil, protocolFailure(fmt.Errorf("stream part %d: %w", index, err))
	}
	callID := call.ID
	if callID == "" {
		callID = generatedID(s.exchange, fmt.Sprintf("toolu_%s_%d", call.Name, s.nextBlock))
	}
	// Gemini rejects a replayed functionCall without its original thought
	// signature, so it has to survive the round-trip through the client — on
	// the block for clients that keep unknown fields, in the cache for the
	// ones that do not.
	antigravityToolSignatures.Remember(callID, signature)

	output := s.closeBlock()
	blockIndex := s.nextBlock
	s.nextBlock++
	toolBlock := map[string]any{
		"type": "tool_use", "id": callID, "name": call.Name, "input": map[string]any{},
	}
	if signature != "" {
		toolBlock["signature"] = signature
	}
	output = append(output,
		antigravityStreamFrame("content_block_start", map[string]any{
			"type": "content_block_start", "index": blockIndex,
			"content_block": toolBlock,
		}),
		antigravityStreamFrame("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)},
		}),
		antigravityStreamFrame("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": blockIndex,
		}),
	)
	s.hasToolUse = true
	s.hasContent = true
	return output, nil
}

func (s *antigravityToAnthropicStream) openBlock(kind string, block map[string]any) model.SSEFrame {
	s.openKind = kind
	s.openIndex = s.nextBlock
	s.nextBlock++
	return antigravityStreamFrame("content_block_start", map[string]any{
		"type": "content_block_start", "index": s.openIndex, "content_block": block,
	})
}

func (s *antigravityToAnthropicStream) closeBlock() []model.SSEFrame {
	if s.openKind == antigravityBlockNone {
		return nil
	}
	index := s.openIndex
	s.openKind = antigravityBlockNone
	return []model.SSEFrame{antigravityStreamFrame("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": index,
	})}
}

func (s *antigravityToAnthropicStream) finalEvents() []model.SSEFrame {
	if s.sentFinal {
		return nil
	}
	s.sentFinal = true
	output := s.closeBlock()

	stopReason := antigravity.StopReason(s.finishReason)
	if s.hasToolUse {
		stopReason = "tool_use"
	}
	usage := antigravityUsage(s.usage, s.cachedTokens)
	delta := map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		},
	}
	if usage.CacheReadInputTokens > 0 {
		delta["usage"].(map[string]any)["cache_read_input_tokens"] = usage.CacheReadInputTokens
	}
	return append(output, antigravityStreamFrame("message_delta", delta))
}

func antigravityStreamFrame(event string, value any) model.SSEFrame {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal stream frame: %v", err))
	}
	return model.SSEFrame{Event: event, Data: data}
}
