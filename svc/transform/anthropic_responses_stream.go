package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/responses"
)

type responsesStreamEvent struct {
	Type      string               `json:"type"`
	Delta     string               `json:"delta,omitempty"`
	Arguments string               `json:"arguments,omitempty"`
	ItemID    string               `json:"item_id,omitempty"`
	CallID    string               `json:"call_id,omitempty"`
	Item      responses.OutputItem `json:"item,omitempty"`
	Response  responses.Response   `json:"response,omitempty"`
}

type anthropicStreamEvent struct {
	Type         string             `json:"type"`
	Index        int                `json:"index,omitempty"`
	Message      anthropic.Response `json:"message,omitempty"`
	ContentBlock anthropic.Content  `json:"content_block,omitempty"`
	Delta        struct {
		Type        string `json:"type,omitempty"`
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
		StopReason  string `json:"stop_reason,omitempty"`
	} `json:"delta,omitempty"`
	Usage anthropic.Usage `json:"usage,omitempty"`
	Error struct {
		Type    string `json:"type,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type responsesToAnthropicBlock struct {
	index     int
	typeName  string
	callID    string
	name      string
	arguments string
	open      bool
}

type responsesToAnthropicStream struct {
	exchange   model.Exchange
	responseID string
	model      string
	started    bool
	terminal   bool
	nextBlock  int
	stopReason string
	usage      anthropic.Usage
	thinking   *responsesToAnthropicBlock
	texts      map[string]*responsesToAnthropicBlock
	tools      map[string]*responsesToAnthropicBlock
	itemToCall map[string]string
}

// NewResponsesToAnthropicStream converts one Responses SSE stream to Anthropic Messages events.
func NewResponsesToAnthropicStream(exchange model.Exchange) (StreamTransform, error) {
	if exchange.NewID == nil {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: nil ID generator"))
	}
	return &responsesToAnthropicStream{
		exchange:   exchange,
		model:      exchange.OriginalRequest.Model,
		stopReason: "end_turn",
		texts:      make(map[string]*responsesToAnthropicBlock),
		tools:      make(map[string]*responsesToAnthropicBlock),
		itemToCall: make(map[string]string),
	}, nil
}

func (s *responsesToAnthropicStream) Push(ctx context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: frame received after terminal event"))
	}
	var event responsesStreamEvent
	if err := json.Unmarshal(frame.Data, &event); err != nil {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: decode %q: %w", frame.Event, err))
	}
	eventType := frame.Event
	if eventType == "" {
		eventType = event.Type
	}

	switch eventType {
	case "response.created", "response.in_progress":
		if err := s.captureResponse(event.Response); err != nil {
			return nil, err
		}
		return s.ensureMessageStart()
	case "response.reasoning_summary_text.delta":
		if event.Delta == "" {
			return nil, nil
		}
		return s.pushReasoning(event.Delta)
	case "response.output_item.added":
		return s.addOutputItem(event.Item)
	case "response.output_text.delta":
		if event.Delta == "" {
			return nil, nil
		}
		return s.pushText(event.ItemID, event.Delta)
	case "response.function_call_arguments.delta":
		return s.pushArguments(event.ItemID, event.CallID, event.Delta)
	case "response.function_call_arguments.done":
		return s.finishArguments(event.ItemID, event.CallID, event.Arguments)
	case "response.output_item.done":
		return s.finishOutputItem(event.Item)
	case "response.completed":
		return s.complete(event.Response)
	case "response.failed":
		return s.fail(event.Response)
	default:
		return nil, nil
	}
}

func (s *responsesToAnthropicStream) Close(ctx context.Context) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: stream ended before terminal event"))
	}
	return nil, nil
}

func (s *responsesToAnthropicStream) captureResponse(response responses.Response) error {
	if response.ID != "" {
		s.responseID = response.ID
	}
	if s.model == "" {
		s.model = response.Model
	}
	if response.Usage != nil {
		usage, err := responsesToAnthropicUsage(response.Usage)
		if err != nil {
			return err
		}
		s.usage = usage
	}
	return nil
}

func (s *responsesToAnthropicStream) ensureMessageStart() ([]model.SSEFrame, error) {
	if s.started {
		return nil, nil
	}
	s.started = true
	if s.responseID == "" {
		s.responseID = s.exchange.NewID()
	}
	payload := struct {
		Type    string `json:"type"`
		Message struct {
			ID           string          `json:"id"`
			Type         string          `json:"type"`
			Role         string          `json:"role"`
			Content      []any           `json:"content"`
			Model        string          `json:"model"`
			StopReason   *string         `json:"stop_reason"`
			StopSequence *string         `json:"stop_sequence"`
			Usage        anthropic.Usage `json:"usage"`
		} `json:"message"`
	}{Type: "message_start"}
	payload.Message.ID = s.responseID
	payload.Message.Type = "message"
	payload.Message.Role = "assistant"
	payload.Message.Content = []any{}
	payload.Message.Model = s.model
	payload.Message.Usage = s.usage
	frame, err := makeStreamFrame("message_start", payload)
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{frame}, nil
}

func (s *responsesToAnthropicStream) pushReasoning(delta string) ([]model.SSEFrame, error) {
	output, err := s.ensureMessageStart()
	if err != nil {
		return nil, err
	}
	if s.thinking == nil || !s.thinking.open {
		closed, closeErr := s.closeActiveTextBlocks()
		if closeErr != nil {
			return nil, closeErr
		}
		output = append(output, closed...)
		s.thinking = &responsesToAnthropicBlock{index: s.allocateBlock(), typeName: "thinking", open: true}
		start, frameErr := anthropicBlockStart(s.thinking.index, anthropic.Content{Type: "thinking", Thinking: ""})
		if frameErr != nil {
			return nil, frameErr
		}
		output = append(output, start)
	}
	deltaFrame, err := makeStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": s.thinking.index,
		"delta": map[string]any{"type": "thinking_delta", "thinking": delta},
	})
	if err != nil {
		return nil, err
	}
	return append(output, deltaFrame), nil
}

func (s *responsesToAnthropicStream) addOutputItem(item responses.OutputItem) ([]model.SSEFrame, error) {
	if item.Type != "function_call" {
		return nil, nil
	}
	if strings.TrimSpace(item.CallID) == "" {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: function call has no call_id"))
	}
	output, err := s.ensureMessageStart()
	if err != nil {
		return nil, err
	}
	closed, err := s.closeNarrativeBlocks()
	if err != nil {
		return nil, err
	}
	output = append(output, closed...)
	if existing := s.tools[item.CallID]; existing != nil {
		return output, nil
	}
	block := &responsesToAnthropicBlock{
		index: s.allocateBlock(), typeName: "tool_use", callID: item.CallID,
		name: item.Name, arguments: item.Arguments, open: true,
	}
	s.tools[item.CallID] = block
	if item.ID != "" {
		s.itemToCall[item.ID] = item.CallID
	}
	s.stopReason = "tool_use"
	start, err := anthropicBlockStart(block.index, anthropic.Content{
		Type: "tool_use", ID: block.callID, Name: block.name, Input: json.RawMessage(`{}`),
	})
	if err != nil {
		return nil, err
	}
	return append(output, start), nil
}

func (s *responsesToAnthropicStream) pushText(itemID, delta string) ([]model.SSEFrame, error) {
	output, err := s.ensureMessageStart()
	if err != nil {
		return nil, err
	}
	if s.thinking != nil && s.thinking.open {
		stop, stopErr := s.closeBlock(s.thinking)
		if stopErr != nil {
			return nil, stopErr
		}
		output = append(output, stop...)
	}
	key := itemID
	if key == "" {
		key = "default"
	}
	block := s.texts[key]
	if block == nil || !block.open {
		block = &responsesToAnthropicBlock{index: s.allocateBlock(), typeName: "text", open: true}
		s.texts[key] = block
		start, frameErr := anthropicBlockStart(block.index, anthropic.Content{Type: "text", Text: ""})
		if frameErr != nil {
			return nil, frameErr
		}
		output = append(output, start)
	}
	deltaFrame, err := makeStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": block.index,
		"delta": map[string]any{"type": "text_delta", "text": delta},
	})
	if err != nil {
		return nil, err
	}
	return append(output, deltaFrame), nil
}

func (s *responsesToAnthropicStream) pushArguments(itemID, callID, delta string) ([]model.SSEFrame, error) {
	block, err := s.resolveTool(itemID, callID)
	if err != nil {
		return nil, err
	}
	if delta == "" {
		return nil, nil
	}
	block.arguments += delta
	frame, err := makeStreamFrame("content_block_delta", map[string]any{
		"type": "content_block_delta", "index": block.index,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": delta},
	})
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{frame}, nil
}

func (s *responsesToAnthropicStream) finishArguments(itemID, callID, arguments string) ([]model.SSEFrame, error) {
	block, err := s.resolveTool(itemID, callID)
	if err != nil {
		return nil, err
	}
	if arguments != "" {
		block.arguments = arguments
	}
	if _, err := validateArguments(block.arguments); err != nil {
		return nil, err
	}
	return nil, nil
}

func (s *responsesToAnthropicStream) finishOutputItem(item responses.OutputItem) ([]model.SSEFrame, error) {
	if item.Type == "function_call" {
		block, err := s.resolveTool(item.ID, item.CallID)
		if err != nil {
			return nil, err
		}
		if item.Arguments != "" {
			block.arguments = item.Arguments
		}
		if _, err := validateArguments(block.arguments); err != nil {
			return nil, err
		}
		return s.closeBlock(block)
	}
	if item.Type == "message" {
		return s.closeBlock(s.texts[item.ID])
	}
	return nil, nil
}

func (s *responsesToAnthropicStream) complete(response responses.Response) ([]model.SSEFrame, error) {
	if err := s.captureResponse(response); err != nil {
		return nil, err
	}
	for _, block := range s.tools {
		if _, err := validateArguments(block.arguments); err != nil {
			return nil, err
		}
	}
	output, err := s.ensureMessageStart()
	if err != nil {
		return nil, err
	}
	closed, err := s.closeAllBlocks()
	if err != nil {
		return nil, err
	}
	output = append(output, closed...)
	if response.Status == "incomplete" && s.stopReason != "tool_use" {
		s.stopReason = "max_tokens"
	}
	delta, err := makeStreamFrame("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": s.stopReason, "stop_sequence": nil},
		"usage": s.usage,
	})
	if err != nil {
		return nil, err
	}
	stop, err := makeStreamFrame("message_stop", map[string]any{"type": "message_stop"})
	if err != nil {
		return nil, err
	}
	s.terminal = true
	return append(output, delta, stop), nil
}

func (s *responsesToAnthropicStream) fail(response responses.Response) ([]model.SSEFrame, error) {
	message := "upstream response failed"
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		message = response.Error.Message
	}
	frame, err := makeStreamFrame("error", map[string]any{
		"type": "error", "error": map[string]any{"type": "upstream_error", "message": message},
	})
	if err != nil {
		return nil, err
	}
	s.terminal = true
	return []model.SSEFrame{frame}, nil
}

func (s *responsesToAnthropicStream) allocateBlock() int {
	index := s.nextBlock
	s.nextBlock++
	return index
}

func (s *responsesToAnthropicStream) resolveTool(itemID, callID string) (*responsesToAnthropicBlock, error) {
	if callID == "" {
		callID = s.itemToCall[itemID]
	}
	block := s.tools[callID]
	if block == nil {
		return nil, protocolFailure(fmt.Errorf("responses to anthropic stream: unknown function call %q", itemID))
	}
	return block, nil
}

func (s *responsesToAnthropicStream) closeNarrativeBlocks() ([]model.SSEFrame, error) {
	var output []model.SSEFrame
	if s.thinking != nil && s.thinking.open {
		frames, err := s.closeBlock(s.thinking)
		if err != nil {
			return nil, err
		}
		output = append(output, frames...)
	}
	text, err := s.closeActiveTextBlocks()
	if err != nil {
		return nil, err
	}
	return append(output, text...), nil
}

func (s *responsesToAnthropicStream) closeActiveTextBlocks() ([]model.SSEFrame, error) {
	blocks := make([]*responsesToAnthropicBlock, 0, len(s.texts))
	for _, block := range s.texts {
		if block.open {
			blocks = append(blocks, block)
		}
	}
	return closeResponsesAnthropicBlocks(blocks)
}

func (s *responsesToAnthropicStream) closeAllBlocks() ([]model.SSEFrame, error) {
	blocks := make([]*responsesToAnthropicBlock, 0, 1+len(s.texts)+len(s.tools))
	if s.thinking != nil && s.thinking.open {
		blocks = append(blocks, s.thinking)
	}
	for _, block := range s.texts {
		if block.open {
			blocks = append(blocks, block)
		}
	}
	for _, block := range s.tools {
		if block.open {
			blocks = append(blocks, block)
		}
	}
	return closeResponsesAnthropicBlocks(blocks)
}

func (s *responsesToAnthropicStream) closeBlock(block *responsesToAnthropicBlock) ([]model.SSEFrame, error) {
	if block == nil || !block.open {
		return nil, nil
	}
	return closeResponsesAnthropicBlocks([]*responsesToAnthropicBlock{block})
}

func closeResponsesAnthropicBlocks(blocks []*responsesToAnthropicBlock) ([]model.SSEFrame, error) {
	sort.Slice(blocks, func(left, right int) bool { return blocks[left].index < blocks[right].index })
	output := make([]model.SSEFrame, 0, len(blocks))
	for _, block := range blocks {
		frame, err := makeStreamFrame("content_block_stop", map[string]any{
			"type": "content_block_stop", "index": block.index,
		})
		if err != nil {
			return nil, err
		}
		block.open = false
		output = append(output, frame)
	}
	return output, nil
}

func anthropicBlockStart(index int, block anthropic.Content) (model.SSEFrame, error) {
	return makeStreamFrame("content_block_start", map[string]any{
		"type": "content_block_start", "index": index, "content_block": block,
	})
}

type anthropicToResponsesBlock struct {
	index     int
	typeName  string
	item      responses.OutputItem
	text      string
	arguments string
	closed    bool
}

type anthropicToResponsesStream struct {
	exchange   model.Exchange
	response   responses.Response
	blocks     map[int]*anthropicToResponsesBlock
	inputUsage anthropic.Usage
	stopReason string
	started    bool
	terminal   bool
}

// NewAnthropicToResponsesStream converts one Anthropic Messages SSE stream to Responses events.
func NewAnthropicToResponsesStream(exchange model.Exchange) (StreamTransform, error) {
	if exchange.NewID == nil {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: nil ID generator"))
	}
	return &anthropicToResponsesStream{
		exchange: exchange,
		response: responses.Response{Object: "response", Model: exchange.OriginalRequest.Model, Status: "in_progress"},
		blocks:   make(map[int]*anthropicToResponsesBlock),
	}, nil
}

func (s *anthropicToResponsesStream) Push(ctx context.Context, frame model.SSEFrame) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.terminal {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: frame received after terminal event"))
	}
	var event anthropicStreamEvent
	if err := json.Unmarshal(frame.Data, &event); err != nil {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: decode %q: %w", frame.Event, err))
	}
	eventType := frame.Event
	if eventType == "" {
		eventType = event.Type
	}
	switch eventType {
	case "message_start":
		return s.start(event.Message)
	case "content_block_start":
		return s.startBlock(event.Index, event.ContentBlock)
	case "content_block_delta":
		return s.pushBlockDelta(event.Index, event.Delta.Type, event.Delta.Text, event.Delta.Thinking, event.Delta.PartialJSON)
	case "content_block_stop":
		return s.stopBlock(event.Index)
	case "message_delta":
		s.stopReason = event.Delta.StopReason
		s.inputUsage.OutputTokens = event.Usage.OutputTokens
		return nil, nil
	case "message_stop":
		return s.complete()
	case "error":
		return s.fail(event.Error.Type, event.Error.Message)
	default:
		return nil, nil
	}
}

func (s *anthropicToResponsesStream) Close(ctx context.Context) ([]model.SSEFrame, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !s.terminal {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: stream ended before terminal event"))
	}
	return nil, nil
}

func (s *anthropicToResponsesStream) start(message anthropic.Response) ([]model.SSEFrame, error) {
	if s.started {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: duplicate message_start"))
	}
	s.started = true
	s.response.ID = message.ID
	if s.response.ID == "" {
		s.response.ID = s.exchange.NewID()
	}
	if s.response.Model == "" {
		s.response.Model = message.Model
	}
	s.inputUsage = message.Usage
	createdResponse := s.response
	createdResponse.Status = "in_progress"
	created, err := makeStreamFrame("response.created", map[string]any{
		"type": "response.created", "response": createdResponse,
	})
	if err != nil {
		return nil, err
	}
	inProgress, err := makeStreamFrame("response.in_progress", map[string]any{
		"type": "response.in_progress", "response": createdResponse,
	})
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{created, inProgress}, nil
}

func (s *anthropicToResponsesStream) startBlock(index int, content anthropic.Content) ([]model.SSEFrame, error) {
	if !s.started {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: content block before message_start"))
	}
	if _, exists := s.blocks[index]; exists {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: duplicate content block %d", index))
	}
	block := &anthropicToResponsesBlock{index: index, typeName: content.Type}
	var output []model.SSEFrame
	var err error
	switch content.Type {
	case "thinking":
		block.item = responses.OutputItem{ID: s.exchange.NewID(), Type: "reasoning"}
		output, err = s.reasoningStart(block)
	case "text":
		block.item = responses.OutputItem{ID: s.exchange.NewID(), Type: "message", Role: "assistant", Status: "in_progress"}
		output, err = s.textStart(block)
	case "tool_use":
		block.item = responses.OutputItem{
			ID: s.exchange.NewID(), Type: "function_call", CallID: content.ID,
			Name: content.Name,
		}
		output, err = s.toolStart(block)
	default:
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: unsupported content block %q", content.Type))
	}
	if err != nil {
		return nil, err
	}
	s.blocks[index] = block
	if content.Thinking != "" {
		more, deltaErr := s.pushBlockDelta(index, "thinking_delta", "", content.Thinking, "")
		return append(output, more...), deltaErr
	}
	if content.Text != "" {
		more, deltaErr := s.pushBlockDelta(index, "text_delta", content.Text, "", "")
		return append(output, more...), deltaErr
	}
	return output, nil
}

func (s *anthropicToResponsesStream) reasoningStart(block *anthropicToResponsesBlock) ([]model.SSEFrame, error) {
	added, err := makeStreamFrame("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": len(s.response.Output), "item": block.item,
	})
	if err != nil {
		return nil, err
	}
	part, err := makeStreamFrame("response.reasoning_summary_part.added", map[string]any{
		"type": "response.reasoning_summary_part.added", "item_id": block.item.ID,
		"output_index": len(s.response.Output), "summary_index": 0,
		"part": responses.Content{Type: "summary_text", Text: ""},
	})
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{added, part}, nil
}

func (s *anthropicToResponsesStream) textStart(block *anthropicToResponsesBlock) ([]model.SSEFrame, error) {
	added, err := makeStreamFrame("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": len(s.response.Output), "item": block.item,
	})
	if err != nil {
		return nil, err
	}
	part, err := makeStreamFrame("response.content_part.added", map[string]any{
		"type": "response.content_part.added", "item_id": block.item.ID,
		"output_index": len(s.response.Output), "content_index": 0,
		"part": responses.Content{Type: "output_text", Text: ""},
	})
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{added, part}, nil
}

func (s *anthropicToResponsesStream) toolStart(block *anthropicToResponsesBlock) ([]model.SSEFrame, error) {
	frame, err := makeStreamFrame("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": len(s.response.Output), "item": block.item,
	})
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{frame}, nil
}

func (s *anthropicToResponsesStream) pushBlockDelta(index int, deltaType, text, thinking, arguments string) ([]model.SSEFrame, error) {
	block := s.blocks[index]
	if block == nil || block.closed {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: delta for unknown block %d", index))
	}
	var eventName string
	var payload map[string]any
	switch deltaType {
	case "thinking_delta":
		if block.typeName != "thinking" {
			return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: thinking delta for %s block", block.typeName))
		}
		block.text += thinking
		eventName = "response.reasoning_summary_text.delta"
		payload = map[string]any{"type": eventName, "item_id": block.item.ID, "summary_index": 0, "delta": thinking}
	case "text_delta":
		if block.typeName != "text" {
			return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: text delta for %s block", block.typeName))
		}
		block.text += text
		eventName = "response.output_text.delta"
		payload = map[string]any{"type": eventName, "item_id": block.item.ID, "content_index": 0, "delta": text}
	case "input_json_delta":
		if block.typeName != "tool_use" {
			return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: input delta for %s block", block.typeName))
		}
		block.arguments += arguments
		eventName = "response.function_call_arguments.delta"
		payload = map[string]any{"type": eventName, "item_id": block.item.ID, "output_index": len(s.response.Output), "delta": arguments}
	default:
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: unsupported delta %q", deltaType))
	}
	frame, err := makeStreamFrame(eventName, payload)
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{frame}, nil
}

func (s *anthropicToResponsesStream) stopBlock(index int) ([]model.SSEFrame, error) {
	block := s.blocks[index]
	if block == nil || block.closed {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: stop for unknown block %d", index))
	}
	block.closed = true
	outputIndex := len(s.response.Output)
	var output []model.SSEFrame
	var err error
	switch block.typeName {
	case "thinking":
		block.item.Summary = responses.ContentList{{Type: "summary_text", Text: block.text}}
		output, err = s.finishReasoning(block, outputIndex)
	case "text":
		block.item.Status = "completed"
		block.item.Content = responses.ContentList{{Type: "output_text", Text: block.text}}
		output, err = s.finishText(block, outputIndex)
	case "tool_use":
		arguments, validationErr := validateArguments(block.arguments)
		if validationErr != nil {
			return nil, validationErr
		}
		block.item.Arguments = string(arguments)
		output, err = s.finishTool(block, outputIndex)
	}
	if err != nil {
		return nil, err
	}
	s.response.Output = append(s.response.Output, block.item)
	return output, nil
}

func (s *anthropicToResponsesStream) finishReasoning(block *anthropicToResponsesBlock, outputIndex int) ([]model.SSEFrame, error) {
	textDone, err := makeStreamFrame("response.reasoning_summary_text.done", map[string]any{
		"type": "response.reasoning_summary_text.done", "item_id": block.item.ID,
		"output_index": outputIndex, "summary_index": 0, "text": block.text,
	})
	if err != nil {
		return nil, err
	}
	partDone, err := makeStreamFrame("response.reasoning_summary_part.done", map[string]any{
		"type": "response.reasoning_summary_part.done", "item_id": block.item.ID,
		"output_index": outputIndex, "summary_index": 0,
		"part": responses.Content{Type: "summary_text", Text: block.text},
	})
	if err != nil {
		return nil, err
	}
	itemDone, err := responseOutputItemDone(block.item, outputIndex)
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{textDone, partDone, itemDone}, nil
}

func (s *anthropicToResponsesStream) finishText(block *anthropicToResponsesBlock, outputIndex int) ([]model.SSEFrame, error) {
	textDone, err := makeStreamFrame("response.output_text.done", map[string]any{
		"type": "response.output_text.done", "item_id": block.item.ID,
		"output_index": outputIndex, "content_index": 0, "text": block.text,
	})
	if err != nil {
		return nil, err
	}
	partDone, err := makeStreamFrame("response.content_part.done", map[string]any{
		"type": "response.content_part.done", "item_id": block.item.ID,
		"output_index": outputIndex, "content_index": 0,
		"part": responses.Content{Type: "output_text", Text: block.text},
	})
	if err != nil {
		return nil, err
	}
	itemDone, err := responseOutputItemDone(block.item, outputIndex)
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{textDone, partDone, itemDone}, nil
}

func (s *anthropicToResponsesStream) finishTool(block *anthropicToResponsesBlock, outputIndex int) ([]model.SSEFrame, error) {
	argumentsDone, err := makeStreamFrame("response.function_call_arguments.done", map[string]any{
		"type": "response.function_call_arguments.done", "item_id": block.item.ID,
		"output_index": outputIndex, "arguments": block.item.Arguments,
	})
	if err != nil {
		return nil, err
	}
	itemDone, err := responseOutputItemDone(block.item, outputIndex)
	if err != nil {
		return nil, err
	}
	return []model.SSEFrame{argumentsDone, itemDone}, nil
}

func responseOutputItemDone(item responses.OutputItem, outputIndex int) (model.SSEFrame, error) {
	return makeStreamFrame("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": outputIndex, "item": item,
	})
}

func (s *anthropicToResponsesStream) complete() ([]model.SSEFrame, error) {
	if !s.started {
		return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: message_stop before message_start"))
	}
	for index, block := range s.blocks {
		if !block.closed {
			return nil, protocolFailure(fmt.Errorf("anthropic to responses stream: block %d still open at message_stop", index))
		}
	}
	s.response.Status = responsesStatusFromAnthropic(s.stopReason)
	if s.stopReason == "max_tokens" {
		s.response.IncompleteDetails = &responses.IncompleteDetails{Reason: "max_output_tokens"}
	}
	cached := s.inputUsage.CacheCreationInputTokens + s.inputUsage.CacheReadInputTokens
	s.response.Usage = &responses.Usage{
		InputTokens: s.inputUsage.InputTokens + cached, OutputTokens: s.inputUsage.OutputTokens,
		TotalTokens:        s.inputUsage.InputTokens + cached + s.inputUsage.OutputTokens,
		InputTokensDetails: &responses.InputTokensDetails{CachedTokens: cached},
	}
	frame, err := makeStreamFrame("response.completed", map[string]any{
		"type": "response.completed", "response": s.response,
	})
	if err != nil {
		return nil, err
	}
	s.terminal = true
	return []model.SSEFrame{frame}, nil
}

func (s *anthropicToResponsesStream) fail(code, message string) ([]model.SSEFrame, error) {
	if code == "" {
		code = "upstream_error"
	}
	if message == "" {
		message = "upstream response failed"
	}
	responseID := s.response.ID
	if responseID == "" {
		responseID = s.exchange.NewID()
	}
	failed := responses.Response{
		ID: responseID, Object: "response", Model: s.response.Model, Status: "failed",
		Error: &responses.ResponseError{Code: code, Message: message},
	}
	frame, err := makeStreamFrame("response.failed", map[string]any{
		"type": "response.failed", "response": failed,
	})
	if err != nil {
		return nil, err
	}
	s.terminal = true
	return []model.SSEFrame{frame}, nil
}

func makeStreamFrame(event string, payload any) (model.SSEFrame, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return model.SSEFrame{}, protocolFailure(fmt.Errorf("encode %s event: %w", event, err))
	}
	return model.SSEFrame{Event: event, Data: body}, nil
}
