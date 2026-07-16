package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/chat"
	"github.com/bizshuk/proxy/protocol/responses"
)

type chatToResponsesTool struct {
	outputIndex int
	itemID      string
	callID      string
	name        string
	arguments   strings.Builder
}

type chatToResponsesStream struct {
	exchange       protocol.Exchange
	responseID     string
	model          string
	started        bool
	terminal       bool
	finishReason   string
	nextOutput     int
	reasoningIndex int
	reasoningID    string
	reasoning      strings.Builder
	messageIndex   int
	messageID      string
	text           strings.Builder
	tools          map[int]*chatToResponsesTool
	usage          *responses.Usage
}

// NewChatToResponsesStream creates a direct Chat-to-Responses stream transform.
func NewChatToResponsesStream(exchange protocol.Exchange) (StreamTransform, error) {
	if exchange.NewID == nil {
		return nil, fmt.Errorf("new Chat-to-Responses stream: nil ID generator")
	}
	return &chatToResponsesStream{
		exchange: exchange, reasoningIndex: -1, messageIndex: -1,
		tools: make(map[int]*chatToResponsesTool),
	}, nil
}

func (s *chatToResponsesStream) Push(ctx context.Context, frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("Chat frame received after terminal event"))
	}
	if bytes.Equal(bytes.TrimSpace(frame.Data), []byte("[DONE]")) {
		return s.complete()
	}
	if len(frame.Data) == 0 {
		return nil, nil
	}
	var chunk chat.StreamChunk
	if err := json.Unmarshal(frame.Data, &chunk); err != nil {
		return nil, protocolFailure(fmt.Errorf("decode Chat stream chunk: %w", err))
	}
	if chunk.ID != "" {
		s.responseID = chunk.ID
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	var output []protocol.SSEFrame
	if !s.started {
		if s.responseID == "" {
			s.responseID = s.exchange.NewID()
		}
		if s.model == "" {
			s.model = s.exchange.OriginalRequest.Model
		}
		created, err := s.event("response.created", map[string]any{
			"type": "response.created", "response": map[string]any{
				"id": s.responseID, "object": "response", "model": s.model, "status": "in_progress",
			},
		})
		if err != nil {
			return nil, err
		}
		inProgress, err := s.event("response.in_progress", map[string]any{
			"type": "response.in_progress", "response": map[string]any{"id": s.responseID, "status": "in_progress"},
		})
		if err != nil {
			return nil, err
		}
		output = append(output, created, inProgress)
		s.started = true
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return nil, protocolFailure(fmt.Errorf("multiple Chat choices are not supported"))
		}
		if delta := choice.Delta.ReasoningContent; delta != "" {
			frames, err := s.pushReasoning(delta)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		if delta := choice.Delta.Content; delta != "" {
			frames, err := s.pushText(delta)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		for _, delta := range choice.Delta.ToolCalls {
			frames, err := s.pushTool(delta)
			if err != nil {
				return nil, err
			}
			output = append(output, frames...)
		}
		if choice.FinishReason != "" {
			s.finishReason = choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		s.usage = &responses.Usage{
			InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens: chunk.Usage.TotalTokens,
		}
		if chunk.Usage.PromptTokensDetails != nil {
			s.usage.InputTokensDetails = &responses.InputTokensDetails{CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens}
		}
	}
	return output, nil
}

func (s *chatToResponsesStream) Close(ctx context.Context) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("Chat stream ended before [DONE]"))
	}
	return nil, nil
}

func (s *chatToResponsesStream) pushReasoning(delta string) ([]protocol.SSEFrame, error) {
	var frames []protocol.SSEFrame
	if s.reasoningIndex < 0 {
		s.reasoningIndex = s.nextOutput
		s.nextOutput++
		s.reasoningID = s.exchange.NewID()
		frame, err := s.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": s.reasoningIndex,
			"item": map[string]any{"id": s.reasoningID, "type": "reasoning", "summary": []any{}},
		})
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	s.reasoning.WriteString(delta)
	frame, err := s.event("response.reasoning_summary_text.delta", map[string]any{
		"type": "response.reasoning_summary_text.delta", "item_id": s.reasoningID,
		"output_index": s.reasoningIndex, "delta": delta,
	})
	if err != nil {
		return nil, err
	}
	return append(frames, frame), nil
}

func (s *chatToResponsesStream) pushText(delta string) ([]protocol.SSEFrame, error) {
	var frames []protocol.SSEFrame
	if s.messageIndex < 0 {
		s.messageIndex = s.nextOutput
		s.nextOutput++
		s.messageID = s.exchange.NewID()
		item, err := s.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": s.messageIndex,
			"item": map[string]any{"id": s.messageID, "type": "message", "role": "assistant", "content": []any{}},
		})
		if err != nil {
			return nil, err
		}
		part, err := s.event("response.content_part.added", map[string]any{
			"type": "response.content_part.added", "item_id": s.messageID,
			"output_index": s.messageIndex, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": ""},
		})
		if err != nil {
			return nil, err
		}
		frames = append(frames, item, part)
	}
	s.text.WriteString(delta)
	frame, err := s.event("response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": s.messageID,
		"output_index": s.messageIndex, "content_index": 0, "delta": delta,
	})
	if err != nil {
		return nil, err
	}
	return append(frames, frame), nil
}

func (s *chatToResponsesStream) pushTool(delta chat.StreamToolCall) ([]protocol.SSEFrame, error) {
	tool := s.tools[delta.Index]
	var frames []protocol.SSEFrame
	if tool == nil {
		tool = &chatToResponsesTool{outputIndex: s.nextOutput, itemID: s.exchange.NewID()}
		s.nextOutput++
		s.tools[delta.Index] = tool
	}
	tool.callID += delta.ID
	tool.name += delta.Function.Name
	if tool.callID == "" {
		tool.callID = s.exchange.NewID()
	}
	if delta.ID != "" || delta.Function.Name != "" {
		frame, err := s.event("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": tool.outputIndex,
			"item": map[string]any{
				"id": tool.itemID, "type": "function_call", "call_id": tool.callID,
				"name": tool.name, "arguments": "",
			},
		})
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	if delta.Function.Arguments != "" {
		tool.arguments.WriteString(delta.Function.Arguments)
		frame, err := s.event("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": tool.itemID,
			"output_index": tool.outputIndex, "delta": delta.Function.Arguments,
		})
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, nil
}

func (s *chatToResponsesStream) complete() ([]protocol.SSEFrame, error) {
	if !s.started || s.finishReason == "" {
		return nil, protocolFailure(fmt.Errorf("Chat [DONE] arrived before finish_reason"))
	}
	output := make([]responses.OutputItem, 0, s.nextOutput)
	if s.reasoningIndex >= 0 {
		output = append(output, responses.OutputItem{
			ID: s.reasoningID, Type: "reasoning",
			Summary: responses.ContentList{{Type: "summary_text", Text: s.reasoning.String()}},
		})
	}
	if s.messageIndex >= 0 {
		output = append(output, responses.OutputItem{
			ID: s.messageID, Type: "message", Role: "assistant", Status: "completed",
			Content: responses.ContentList{{Type: "output_text", Text: s.text.String()}},
		})
	}
	toolIndices := make([]int, 0, len(s.tools))
	for index := range s.tools {
		toolIndices = append(toolIndices, index)
	}
	sort.Ints(toolIndices)
	for _, index := range toolIndices {
		tool := s.tools[index]
		arguments := tool.arguments.String()
		if !json.Valid([]byte(arguments)) {
			return nil, protocolFailure(fmt.Errorf("Chat tool arguments are not valid JSON"))
		}
		output = append(output, responses.OutputItem{
			ID: tool.itemID, Type: "function_call", CallID: tool.callID,
			Name: tool.name, Arguments: arguments,
		})
	}
	response := responses.Response{
		ID: s.responseID, Object: "response", Model: s.exchange.OriginalRequest.Model,
		Output: output, Status: "completed", Usage: s.usage,
	}
	if s.finishReason == "length" {
		response.Status = "incomplete"
		response.IncompleteDetails = &responses.IncompleteDetails{Reason: "max_output_tokens"}
	}
	frame, err := s.event("response.completed", map[string]any{"type": "response.completed", "response": response})
	if err != nil {
		return nil, err
	}
	s.terminal = true
	return []protocol.SSEFrame{frame}, nil
}

func (s *chatToResponsesStream) event(name string, payload any) (protocol.SSEFrame, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return protocol.SSEFrame{}, protocolFailure(fmt.Errorf("encode %s: %w", name, err))
	}
	return protocol.SSEFrame{Event: name, Data: body}, nil
}

type responsesToChatStream struct {
	exchange    protocol.Exchange
	responseID  string
	model       string
	started     bool
	terminal    bool
	hasTool     bool
	toolIndices map[string]int
	nextTool    int
	seenText    bool
	seenReason  bool
}

// NewResponsesToChatStream creates a direct Responses-to-Chat stream transform.
func NewResponsesToChatStream(exchange protocol.Exchange) (StreamTransform, error) {
	return &responsesToChatStream{exchange: exchange, toolIndices: make(map[string]int)}, nil
}

func (s *responsesToChatStream) Push(ctx context.Context, frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("Responses frame received after terminal event"))
	}
	event := frame.Event
	if event == "" {
		event = dataType(frame.Data)
	}
	switch event {
	case "response.created", "response.in_progress":
		var payload struct {
			Response responses.Response `json:"response"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return nil, protocolFailure(fmt.Errorf("decode %s: %w", event, err))
		}
		if payload.Response.ID != "" {
			s.responseID = payload.Response.ID
		}
		if payload.Response.Model != "" {
			s.model = payload.Response.Model
		}
		if s.started || event == "response.in_progress" {
			return nil, nil
		}
		s.started = true
		return s.chatFrames(chat.StreamDelta{Role: "assistant"}, "", nil)
	case "response.reasoning_summary_text.delta":
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return nil, protocolFailure(fmt.Errorf("decode reasoning delta: %w", err))
		}
		s.seenReason = true
		return s.chatFrames(chat.StreamDelta{ReasoningContent: payload.Delta}, "", nil)
	case "response.output_text.delta":
		var payload struct {
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return nil, protocolFailure(fmt.Errorf("decode output text delta: %w", err))
		}
		s.seenText = true
		return s.chatFrames(chat.StreamDelta{Content: payload.Delta}, "", nil)
	case "response.output_item.added":
		var payload struct {
			Item responses.OutputItem `json:"item"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return nil, protocolFailure(fmt.Errorf("decode output item: %w", err))
		}
		if payload.Item.Type != "function_call" {
			return nil, nil
		}
		index := s.nextTool
		s.nextTool++
		s.toolIndices[payload.Item.ID] = index
		s.hasTool = true
		return s.chatFrames(chat.StreamDelta{ToolCalls: []chat.StreamToolCall{{
			Index: index, ID: payload.Item.CallID, Type: "function",
			Function: chat.FunctionCall{Name: payload.Item.Name},
		}}}, "", nil)
	case "response.function_call_arguments.delta":
		var payload struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return nil, protocolFailure(fmt.Errorf("decode function arguments delta: %w", err))
		}
		index, ok := s.toolIndices[payload.ItemID]
		if !ok {
			return nil, protocolFailure(fmt.Errorf("arguments delta for unknown Responses item %q", payload.ItemID))
		}
		return s.chatFrames(chat.StreamDelta{ToolCalls: []chat.StreamToolCall{{
			Index: index, Function: chat.FunctionCall{Arguments: payload.Delta},
		}}}, "", nil)
	case "response.completed":
		return s.complete(frame)
	case "response.failed", "error":
		errorBody := []byte(`{"error":{"type":"api_error","code":"stream_error","message":"stream terminated"}}`)
		s.terminal = true
		return []protocol.SSEFrame{{Data: errorBody}, {Data: []byte("[DONE]")}}, nil
	case "response.output_item.done", "response.content_part.added", "response.content_part.done", "response.output_text.done", "response.function_call_arguments.done", "response.reasoning_summary_text.done", "response.reasoning_summary_part.added", "response.reasoning_summary_part.done":
		return nil, nil
	default:
		return nil, protocolFailure(fmt.Errorf("unsupported Responses event %q", event))
	}
}

func (s *responsesToChatStream) Close(ctx context.Context) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("Responses stream ended before terminal event"))
	}
	return nil, nil
}

func (s *responsesToChatStream) complete(frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	var payload struct {
		Response responses.Response `json:"response"`
	}
	if err := json.Unmarshal(frame.Data, &payload); err != nil {
		return nil, protocolFailure(fmt.Errorf("decode response.completed: %w", err))
	}
	if !s.started {
		s.started = true
		s.responseID = payload.Response.ID
		s.model = payload.Response.Model
	}
	var output []protocol.SSEFrame
	for _, item := range payload.Response.Output {
		switch item.Type {
		case "reasoning":
			if !s.seenReason {
				for _, part := range item.Summary {
					frames, err := s.chatFrames(chat.StreamDelta{ReasoningContent: part.Text}, "", nil)
					if err != nil {
						return nil, err
					}
					output = append(output, frames...)
				}
			}
		case "message":
			if !s.seenText {
				for _, part := range item.Content {
					frames, err := s.chatFrames(chat.StreamDelta{Content: part.Text}, "", nil)
					if err != nil {
						return nil, err
					}
					output = append(output, frames...)
				}
			}
		case "function_call":
			s.hasTool = true
		}
	}
	finish := "stop"
	if s.hasTool {
		finish = "tool_calls"
	} else if payload.Response.Status == "incomplete" {
		finish = "length"
	}
	usage := &chat.Usage{}
	if payload.Response.Usage != nil {
		usage.PromptTokens = payload.Response.Usage.InputTokens
		usage.CompletionTokens = payload.Response.Usage.OutputTokens
		usage.TotalTokens = payload.Response.Usage.TotalTokens
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		if payload.Response.Usage.InputTokensDetails != nil {
			usage.PromptTokensDetails = &chat.UsageDetails{CachedTokens: payload.Response.Usage.InputTokensDetails.CachedTokens}
		}
	}
	frames, err := s.chatFrames(chat.StreamDelta{}, finish, usage)
	if err != nil {
		return nil, err
	}
	output = append(output, frames...)
	output = append(output, protocol.SSEFrame{Data: []byte("[DONE]")})
	s.terminal = true
	return output, nil
}

func (s *responsesToChatStream) chatFrames(delta chat.StreamDelta, finish string, usage *chat.Usage) ([]protocol.SSEFrame, error) {
	chunk := chat.StreamChunk{
		ID: s.responseID, Object: "chat.completion.chunk", Model: s.exchange.OriginalRequest.Model,
		Choices: []chat.StreamChoice{{Index: 0, Delta: delta, FinishReason: finish}}, Usage: usage,
	}
	if chunk.Model == "" {
		chunk.Model = s.model
	}
	body, err := chat.Encode(chunk)
	if err != nil {
		return nil, protocolFailure(err)
	}
	return []protocol.SSEFrame{{Data: body}}, nil
}
