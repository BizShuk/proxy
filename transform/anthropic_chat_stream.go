package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/anthropic"
	"github.com/bizshuk/proxy/protocol/chat"
)

type chatToAnthropicToolState struct {
	blockIndex int
	callID     string
	name       string
	arguments  strings.Builder
	partials   []string
}

type chatToAnthropicStream struct {
	exchange      protocol.Exchange
	messageID     string
	started       bool
	terminal      bool
	nextBlock     int
	thinkingBlock int
	thinkingOpen  bool
	textBlock     int
	textOpen      bool
	tools         map[int]*chatToAnthropicToolState
	usage         chat.Usage
	stopReason    string
}

// NewChatToAnthropicStream creates one isolated Chat-to-Anthropic stream transform.
func NewChatToAnthropicStream(exchange protocol.Exchange) (StreamTransform, error) {
	return &chatToAnthropicStream{
		exchange:      exchange,
		thinkingBlock: -1,
		textBlock:     -1,
		tools:         make(map[int]*chatToAnthropicToolState),
	}, nil
}

func (s *chatToAnthropicStream) Push(ctx context.Context, frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if string(frame.Data) == "[DONE]" {
		if !s.terminal {
			return nil, protocolFailure(fmt.Errorf("Chat stream ended before a finish reason"))
		}
		return nil, nil
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("Chat frame received after terminal event"))
	}
	var errorEnvelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(frame.Data, &errorEnvelope); err == nil && len(errorEnvelope.Error) > 0 {
		return s.pushError(errorEnvelope.Error)
	}

	var chunk chat.StreamChunk
	if err := json.Unmarshal(frame.Data, &chunk); err != nil {
		return nil, protocolFailure(fmt.Errorf("decode Chat stream chunk: %w", err))
	}
	if chunk.ID != "" && s.messageID == "" {
		s.messageID = chunk.ID
	}
	if chunk.Usage != nil {
		s.usage = *chunk.Usage
	}

	var output []protocol.SSEFrame
	if len(chunk.Choices) > 0 {
		output = append(output, s.ensureMessageStart()...)
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return nil, protocolFailure(fmt.Errorf("unsupported Chat choice index %d", choice.Index))
		}
		frames, err := s.pushChoice(choice)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	return output, nil
}

func (s *chatToAnthropicStream) Close(ctx context.Context) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("Chat stream ended before terminal event"))
	}
	return nil, nil
}

func (s *chatToAnthropicStream) pushError(raw json.RawMessage) ([]protocol.SSEFrame, error) {
	if !json.Valid(raw) {
		return nil, protocolFailure(fmt.Errorf("Chat error chunk has invalid error payload"))
	}
	data, err := json.Marshal(struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}{Type: "error", Error: raw})
	if err != nil {
		return nil, protocolFailure(fmt.Errorf("encode Anthropic stream error: %w", err))
	}
	s.terminal = true
	return []protocol.SSEFrame{{Event: "error", Data: data}}, nil
}

func (s *chatToAnthropicStream) pushChoice(choice chat.StreamChoice) ([]protocol.SSEFrame, error) {
	var output []protocol.SSEFrame
	if choice.Delta.ReasoningContent != "" {
		frames, err := s.pushThinking(choice.Delta.ReasoningContent)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	if choice.Delta.Content != "" {
		frames, err := s.pushText(choice.Delta.Content)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	for _, toolCall := range choice.Delta.ToolCalls {
		frames, err := s.pushTool(toolCall)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	if choice.FinishReason != "" {
		s.stopReason = chatToAnthropicStop(choice.FinishReason)
		frames, err := s.finish()
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	return output, nil
}

func (s *chatToAnthropicStream) ensureMessageStart() []protocol.SSEFrame {
	if s.started {
		return nil
	}
	s.started = true
	if s.messageID == "" {
		s.messageID = generatedID(s.exchange, "msg_stream")
	}
	payload := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         s.exchange.OriginalRequest.Model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         anthropic.Usage{},
		},
	}
	return []protocol.SSEFrame{anthropicChatStreamFrame("message_start", payload)}
}

func (s *chatToAnthropicStream) pushThinking(delta string) ([]protocol.SSEFrame, error) {
	if s.textOpen || len(s.tools) > 0 {
		return nil, protocolFailure(fmt.Errorf("Chat reasoning delta arrived after content or tool output"))
	}
	var output []protocol.SSEFrame
	if !s.thinkingOpen {
		s.thinkingBlock = s.nextBlock
		s.nextBlock++
		s.thinkingOpen = true
		output = append(output, anthropicChatStreamFrame("content_block_start", map[string]any{
			"type": "content_block_start", "index": s.thinkingBlock,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		}))
	}
	output = append(output, anthropicChatStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": s.thinkingBlock,
		"delta": map[string]any{"type": "thinking_delta", "thinking": delta},
	}))
	return output, nil
}

func (s *chatToAnthropicStream) pushText(delta string) ([]protocol.SSEFrame, error) {
	if len(s.tools) > 0 {
		return nil, protocolFailure(fmt.Errorf("Chat content delta arrived after tool output"))
	}
	output := s.closeThinking()
	if !s.textOpen {
		s.textBlock = s.nextBlock
		s.nextBlock++
		s.textOpen = true
		output = append(output, anthropicChatStreamFrame("content_block_start", map[string]any{
			"type": "content_block_start", "index": s.textBlock,
			"content_block": map[string]any{"type": "text", "text": ""},
		}))
	}
	output = append(output, anthropicChatStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": s.textBlock,
		"delta": map[string]any{"type": "text_delta", "text": delta},
	}))
	return output, nil
}

func (s *chatToAnthropicStream) pushTool(call chat.StreamToolCall) ([]protocol.SSEFrame, error) {
	output := append(s.closeThinking(), s.closeText()...)
	state, found := s.tools[call.Index]
	if !found {
		if call.ID == "" || call.Function.Name == "" {
			return nil, protocolFailure(fmt.Errorf("first Chat tool delta %d requires id and name", call.Index))
		}
		state = &chatToAnthropicToolState{
			blockIndex: s.nextBlock,
			callID:     call.ID,
			name:       call.Function.Name,
		}
		s.nextBlock++
		s.tools[call.Index] = state
	}
	if call.ID != "" && call.ID != state.callID {
		return nil, protocolFailure(fmt.Errorf("Chat tool delta %d changed call id", call.Index))
	}
	if call.Function.Name != "" && call.Function.Name != state.name {
		return nil, protocolFailure(fmt.Errorf("Chat tool delta %d changed function name", call.Index))
	}
	if call.Function.Arguments != "" {
		state.arguments.WriteString(call.Function.Arguments)
		state.partials = append(state.partials, call.Function.Arguments)
	}
	return output, nil
}

func (s *chatToAnthropicStream) finish() ([]protocol.SSEFrame, error) {
	output := append(s.closeThinking(), s.closeText()...)
	toolIndexes := make([]int, 0, len(s.tools))
	for index := range s.tools {
		toolIndexes = append(toolIndexes, index)
	}
	sort.Slice(toolIndexes, func(i, j int) bool {
		return s.tools[toolIndexes[i]].blockIndex < s.tools[toolIndexes[j]].blockIndex
	})
	for _, index := range toolIndexes {
		tool := s.tools[index]
		arguments := tool.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		if !json.Valid([]byte(arguments)) {
			return nil, protocolFailure(fmt.Errorf("Chat tool %d arguments are not valid JSON", index))
		}
		output = append(output, anthropicChatStreamFrame("content_block_start", map[string]any{
			"type": "content_block_start", "index": tool.blockIndex,
			"content_block": map[string]any{
				"type": "tool_use", "id": tool.callID, "name": tool.name, "input": map[string]any{},
			},
		}))
		for _, partial := range tool.partials {
			output = append(output, anthropicChatStreamFrame("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": tool.blockIndex,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": partial},
			}))
		}
		output = append(output, anthropicChatStreamFrame("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": tool.blockIndex,
		}))
	}
	output = append(output,
		anthropicChatStreamFrame("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": s.stopReason, "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": s.usage.CompletionTokens},
		}),
		anthropicChatStreamFrame("message_stop", map[string]any{"type": "message_stop"}),
	)
	s.terminal = true
	return output, nil
}

func (s *chatToAnthropicStream) closeThinking() []protocol.SSEFrame {
	if !s.thinkingOpen {
		return nil
	}
	s.thinkingOpen = false
	return []protocol.SSEFrame{anthropicChatStreamFrame("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": s.thinkingBlock,
	})}
}

func (s *chatToAnthropicStream) closeText() []protocol.SSEFrame {
	if !s.textOpen {
		return nil
	}
	s.textOpen = false
	return []protocol.SSEFrame{anthropicChatStreamFrame("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": s.textBlock,
	})}
}

func anthropicChatStreamFrame(event string, value any) protocol.SSEFrame {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal stream frame: %v", err))
	}
	return protocol.SSEFrame{Event: event, Data: data}
}

type anthropicToChatBlockState struct {
	kind      string
	toolIndex int
	callID    string
	name      string
	arguments strings.Builder
}

type anthropicToChatStream struct {
	exchange  protocol.Exchange
	messageID string
	model     string
	started   bool
	stopSeen  bool
	terminal  bool
	nextTool  int
	blocks    map[int]*anthropicToChatBlockState
	usage     anthropic.Usage
}

type anthropicToChatEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string          `json:"id"`
		Model string          `json:"model"`
		Usage anthropic.Usage `json:"usage"`
	} `json:"message"`
	Index        int               `json:"index"`
	ContentBlock anthropic.Content `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Thinking    string `json:"thinking"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropic.Usage `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// NewAnthropicToChatStream creates one isolated Anthropic-to-Chat stream transform.
func NewAnthropicToChatStream(exchange protocol.Exchange) (StreamTransform, error) {
	return &anthropicToChatStream{
		exchange: exchange,
		model:    exchange.OriginalRequest.Model,
		blocks:   make(map[int]*anthropicToChatBlockState),
	}, nil
}

func (s *anthropicToChatStream) Push(ctx context.Context, frame protocol.SSEFrame) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("Anthropic frame received after terminal event"))
	}
	var event anthropicToChatEvent
	if err := json.Unmarshal(frame.Data, &event); err != nil {
		return nil, protocolFailure(fmt.Errorf("decode Anthropic stream event: %w", err))
	}
	eventType := frame.Event
	if eventType == "" {
		eventType = event.Type
	}
	if event.Type != "" && eventType != event.Type {
		return nil, protocolFailure(fmt.Errorf("Anthropic SSE event %q does not match payload type %q", eventType, event.Type))
	}

	switch eventType {
	case "message_start":
		return s.start(event)
	case "content_block_start":
		return s.startBlock(event)
	case "content_block_delta":
		return s.pushBlockDelta(event)
	case "content_block_stop":
		return nil, s.stopBlock(event.Index)
	case "message_delta":
		return s.pushMessageDelta(event)
	case "message_stop":
		return s.stopMessage()
	case "error":
		return s.pushError(event.Error)
	default:
		return nil, protocolFailure(fmt.Errorf("unsupported Anthropic stream event %q", eventType))
	}
}

func (s *anthropicToChatStream) Close(ctx context.Context) ([]protocol.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("Anthropic stream ended before terminal event"))
	}
	return nil, nil
}

func (s *anthropicToChatStream) start(event anthropicToChatEvent) ([]protocol.SSEFrame, error) {
	if s.started {
		return nil, protocolFailure(fmt.Errorf("duplicate Anthropic message_start"))
	}
	s.started = true
	s.messageID = event.Message.ID
	if s.messageID == "" {
		s.messageID = generatedID(s.exchange, "chat_stream")
	}
	if s.model == "" {
		s.model = event.Message.Model
	}
	s.usage = event.Message.Usage
	return []protocol.SSEFrame{s.chatFrame(chat.StreamDelta{Role: "assistant"}, "", nil)}, nil
}

func (s *anthropicToChatStream) startBlock(event anthropicToChatEvent) ([]protocol.SSEFrame, error) {
	if !s.started {
		return nil, protocolFailure(fmt.Errorf("Anthropic content block started before message_start"))
	}
	if _, found := s.blocks[event.Index]; found {
		return nil, protocolFailure(fmt.Errorf("duplicate Anthropic content block %d", event.Index))
	}
	block := &anthropicToChatBlockState{kind: event.ContentBlock.Type, toolIndex: -1}
	s.blocks[event.Index] = block
	switch event.ContentBlock.Type {
	case "thinking", "text":
		return nil, nil
	case "tool_use":
		if event.ContentBlock.ID == "" || event.ContentBlock.Name == "" {
			return nil, protocolFailure(fmt.Errorf("Anthropic tool block %d requires id and name", event.Index))
		}
		block.toolIndex = s.nextTool
		block.callID = event.ContentBlock.ID
		block.name = event.ContentBlock.Name
		s.nextTool++
		delta := chat.StreamDelta{ToolCalls: []chat.StreamToolCall{{
			Index: block.toolIndex,
			ID:    block.callID,
			Type:  "function",
			Function: chat.FunctionCall{
				Name: block.name,
			},
		}}}
		return []protocol.SSEFrame{s.chatFrame(delta, "", nil)}, nil
	default:
		return nil, protocolFailure(fmt.Errorf("unsupported Anthropic content block %q", event.ContentBlock.Type))
	}
}

func (s *anthropicToChatStream) pushBlockDelta(event anthropicToChatEvent) ([]protocol.SSEFrame, error) {
	block, found := s.blocks[event.Index]
	if !found {
		return nil, protocolFailure(fmt.Errorf("Anthropic delta references unknown block %d", event.Index))
	}
	var delta chat.StreamDelta
	switch event.Delta.Type {
	case "thinking_delta":
		if block.kind != "thinking" {
			return nil, protocolFailure(fmt.Errorf("thinking delta references %s block %d", block.kind, event.Index))
		}
		delta.ReasoningContent = event.Delta.Thinking
	case "text_delta":
		if block.kind != "text" {
			return nil, protocolFailure(fmt.Errorf("text delta references %s block %d", block.kind, event.Index))
		}
		delta.Content = event.Delta.Text
	case "input_json_delta":
		if block.kind != "tool_use" {
			return nil, protocolFailure(fmt.Errorf("input JSON delta references %s block %d", block.kind, event.Index))
		}
		block.arguments.WriteString(event.Delta.PartialJSON)
		delta.ToolCalls = []chat.StreamToolCall{{
			Index: block.toolIndex,
			Function: chat.FunctionCall{
				Arguments: event.Delta.PartialJSON,
			},
		}}
	default:
		return nil, protocolFailure(fmt.Errorf("unsupported Anthropic content delta %q", event.Delta.Type))
	}
	return []protocol.SSEFrame{s.chatFrame(delta, "", nil)}, nil
}

func (s *anthropicToChatStream) stopBlock(index int) error {
	block, found := s.blocks[index]
	if !found {
		return protocolFailure(fmt.Errorf("Anthropic stop references unknown block %d", index))
	}
	if block.kind == "tool_use" {
		arguments := block.arguments.String()
		if arguments == "" {
			arguments = "{}"
		}
		if !json.Valid([]byte(arguments)) {
			return protocolFailure(fmt.Errorf("Anthropic tool block %d arguments are not valid JSON", index))
		}
	}
	delete(s.blocks, index)
	return nil
}

func (s *anthropicToChatStream) pushMessageDelta(event anthropicToChatEvent) ([]protocol.SSEFrame, error) {
	if !s.started {
		return nil, protocolFailure(fmt.Errorf("Anthropic message_delta arrived before message_start"))
	}
	if len(s.blocks) != 0 {
		return nil, protocolFailure(fmt.Errorf("Anthropic message_delta arrived with open content blocks"))
	}
	if event.Delta.StopReason == "" {
		return nil, protocolFailure(fmt.Errorf("Anthropic message_delta requires stop_reason"))
	}
	s.stopSeen = true
	s.usage.OutputTokens = event.Usage.OutputTokens
	cached := s.usage.CacheCreationInputTokens + s.usage.CacheReadInputTokens
	usage := &chat.Usage{
		PromptTokens:     s.usage.InputTokens + cached,
		CompletionTokens: s.usage.OutputTokens,
		TotalTokens:      s.usage.InputTokens + cached + s.usage.OutputTokens,
	}
	return []protocol.SSEFrame{s.chatFrame(chat.StreamDelta{}, anthropicToChatStop(event.Delta.StopReason), usage)}, nil
}

func (s *anthropicToChatStream) stopMessage() ([]protocol.SSEFrame, error) {
	if !s.stopSeen {
		return nil, protocolFailure(fmt.Errorf("Anthropic message_stop arrived before stop_reason"))
	}
	s.terminal = true
	return []protocol.SSEFrame{{Data: []byte("[DONE]")}}, nil
}

func (s *anthropicToChatStream) pushError(raw json.RawMessage) ([]protocol.SSEFrame, error) {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, protocolFailure(fmt.Errorf("Anthropic error event has invalid error payload"))
	}
	data, err := json.Marshal(struct {
		Error json.RawMessage `json:"error"`
	}{Error: raw})
	if err != nil {
		return nil, protocolFailure(fmt.Errorf("encode Chat stream error: %w", err))
	}
	s.terminal = true
	return []protocol.SSEFrame{{Data: data}, {Data: []byte("[DONE]")}}, nil
}

func (s *anthropicToChatStream) chatFrame(delta chat.StreamDelta, finishReason string, usage *chat.Usage) protocol.SSEFrame {
	return anthropicChatStreamFrame("", chat.StreamChunk{
		ID:      s.messageID,
		Object:  "chat.completion.chunk",
		Model:   s.model,
		Choices: []chat.StreamChoice{{Index: 0, Delta: delta, FinishReason: finishReason}},
		Usage:   usage,
	})
}
