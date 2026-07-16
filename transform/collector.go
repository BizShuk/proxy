package transform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/anthropic"
	"github.com/bizshuk/proxy/protocol/chat"
	"github.com/bizshuk/proxy/protocol/responses"
)

// StreamCollector folds one complete source-format SSE lifecycle into JSON.
type StreamCollector interface {
	Push(context.Context, protocol.SSEFrame) error
	Close(context.Context) (protocol.TransformResult, error)
}

// NewStreamCollector creates an isolated collector for one source protocol.
func NewStreamCollector(format protocol.Format, exchange protocol.Exchange) (StreamCollector, error) {
	switch format {
	case protocol.FORMAT_ANTHROPIC_MESSAGES:
		return &anthropicCollector{
			exchange: exchange, blocks: make(map[int]anthropic.Content),
			toolArguments: make(map[int]*strings.Builder), open: make(map[int]bool),
		}, nil
	case protocol.FORMAT_OPENAI_CHAT:
		return &chatCollector{exchange: exchange, tools: make(map[int]*chatCollectedTool)}, nil
	case protocol.FORMAT_OPENAI_RESPONSES:
		return &responsesCollector{exchange: exchange}, nil
	default:
		return nil, fmt.Errorf("new stream collector: unknown format %q", format)
	}
}

type anthropicCollector struct {
	exchange      protocol.Exchange
	response      anthropic.Response
	blocks        map[int]anthropic.Content
	toolArguments map[int]*strings.Builder
	open          map[int]bool
	started       bool
	terminal      bool
}

func (c *anthropicCollector) Push(ctx context.Context, frame protocol.SSEFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.terminal {
		return protocolFailure(fmt.Errorf("Anthropic collector received frame after terminal event"))
	}
	event := frame.Event
	if event == "" {
		event = dataType(frame.Data)
	}
	switch event {
	case "message_start":
		if c.started {
			return protocolFailure(fmt.Errorf("duplicate Anthropic message_start"))
		}
		var payload struct {
			Message anthropic.Response `json:"message"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode Anthropic message_start: %w", err))
		}
		c.response = payload.Message
		c.response.Content = nil
		c.started = true
	case "content_block_start":
		var payload struct {
			Index        int               `json:"index"`
			ContentBlock anthropic.Content `json:"content_block"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode Anthropic content_block_start: %w", err))
		}
		if c.open[payload.Index] {
			return protocolFailure(fmt.Errorf("duplicate open Anthropic content block %d", payload.Index))
		}
		if payload.ContentBlock.Type == "tool_use" {
			payload.ContentBlock.Input = nil
			c.toolArguments[payload.Index] = &strings.Builder{}
		}
		c.blocks[payload.Index] = payload.ContentBlock
		c.open[payload.Index] = true
	case "content_block_delta":
		var payload struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode Anthropic content_block_delta: %w", err))
		}
		if !c.open[payload.Index] {
			return protocolFailure(fmt.Errorf("delta for unopened Anthropic content block %d", payload.Index))
		}
		block := c.blocks[payload.Index]
		switch payload.Delta.Type {
		case "text_delta":
			block.Text += payload.Delta.Text
		case "thinking_delta":
			block.Thinking += payload.Delta.Thinking
		case "input_json_delta":
			builder := c.toolArguments[payload.Index]
			if builder == nil {
				return protocolFailure(fmt.Errorf("tool delta for non-tool Anthropic block %d", payload.Index))
			}
			builder.WriteString(payload.Delta.PartialJSON)
		default:
			return protocolFailure(fmt.Errorf("unsupported Anthropic delta %q", payload.Delta.Type))
		}
		c.blocks[payload.Index] = block
	case "content_block_stop":
		var payload struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode Anthropic content_block_stop: %w", err))
		}
		if !c.open[payload.Index] {
			return protocolFailure(fmt.Errorf("stop for unopened Anthropic content block %d", payload.Index))
		}
		block := c.blocks[payload.Index]
		if builder := c.toolArguments[payload.Index]; builder != nil {
			arguments := builder.String()
			if arguments == "" {
				arguments = "{}"
			}
			if !json.Valid([]byte(arguments)) {
				return protocolFailure(fmt.Errorf("Anthropic tool arguments are not valid JSON"))
			}
			block.Input = json.RawMessage(arguments)
			c.blocks[payload.Index] = block
		}
		delete(c.open, payload.Index)
	case "message_delta":
		var payload struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage anthropic.Usage `json:"usage"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode Anthropic message_delta: %w", err))
		}
		c.response.StopReason = payload.Delta.StopReason
		if payload.Usage.OutputTokens != 0 {
			c.response.Usage.OutputTokens = payload.Usage.OutputTokens
		}
	case "message_stop":
		if !c.started || len(c.open) != 0 {
			return protocolFailure(fmt.Errorf("Anthropic message_stop with incomplete lifecycle"))
		}
		c.terminal = true
	case "error":
		return protocolFailure(fmt.Errorf("Anthropic stream failed"))
	case "ping", "":
		return nil
	default:
		return protocolFailure(fmt.Errorf("unsupported Anthropic event %q", event))
	}
	return nil
}

func (c *anthropicCollector) Close(ctx context.Context) (protocol.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TransformResult{}, err
	}
	if !c.terminal {
		return protocol.TransformResult{}, protocolFailure(fmt.Errorf("Anthropic stream ended before message_stop"))
	}
	indices := make([]int, 0, len(c.blocks))
	for index := range c.blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		c.response.Content = append(c.response.Content, c.blocks[index])
	}
	if model := c.exchange.OriginalRequest.Model; model != "" {
		c.response.Model = model
	}
	body, err := anthropic.Encode(c.response)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	return protocol.TransformResult{Body: body}, nil
}

type chatCollectedTool struct {
	id        string
	typeName  string
	name      string
	arguments strings.Builder
}

type chatCollector struct {
	exchange protocol.Exchange
	response chat.Response
	message  chat.Message
	tools    map[int]*chatCollectedTool
	finish   string
	terminal bool
}

func (c *chatCollector) Push(ctx context.Context, frame protocol.SSEFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.terminal {
		return protocolFailure(fmt.Errorf("Chat collector received frame after terminal event"))
	}
	if bytes.Equal(bytes.TrimSpace(frame.Data), []byte("[DONE]")) {
		if c.finish == "" {
			return protocolFailure(fmt.Errorf("Chat stream ended without finish_reason"))
		}
		c.terminal = true
		return nil
	}
	if len(frame.Data) == 0 {
		return nil
	}
	var chunk chat.StreamChunk
	if err := json.Unmarshal(frame.Data, &chunk); err != nil {
		return protocolFailure(fmt.Errorf("decode Chat stream chunk: %w", err))
	}
	if chunk.ID != "" {
		c.response.ID = chunk.ID
	}
	if chunk.Model != "" {
		c.response.Model = chunk.Model
	}
	if chunk.Object != "" {
		c.response.Object = strings.TrimSuffix(chunk.Object, ".chunk")
	}
	if chunk.Created != 0 {
		c.response.Created = chunk.Created
	}
	if chunk.Usage != nil {
		c.response.Usage = *chunk.Usage
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return protocolFailure(fmt.Errorf("multiple Chat choices are not supported"))
		}
		if choice.Delta.Role != "" {
			c.message.Role = choice.Delta.Role
		}
		if choice.Delta.Content != "" {
			if c.message.Content == nil {
				c.message.Content = chat.TextContent("")
			}
			c.message.Content.Text += choice.Delta.Content
		}
		c.message.ReasoningContent += choice.Delta.ReasoningContent
		for _, delta := range choice.Delta.ToolCalls {
			tool := c.tools[delta.Index]
			if tool == nil {
				tool = &chatCollectedTool{}
				c.tools[delta.Index] = tool
			}
			tool.id += delta.ID
			tool.typeName += delta.Type
			tool.name += delta.Function.Name
			tool.arguments.WriteString(delta.Function.Arguments)
		}
		if choice.FinishReason != "" {
			c.finish = choice.FinishReason
		}
	}
	return nil
}

func (c *chatCollector) Close(ctx context.Context) (protocol.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TransformResult{}, err
	}
	if !c.terminal {
		return protocol.TransformResult{}, protocolFailure(fmt.Errorf("Chat stream ended before [DONE]"))
	}
	indices := make([]int, 0, len(c.tools))
	for index := range c.tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		tool := c.tools[index]
		arguments := tool.arguments.String()
		if !json.Valid([]byte(arguments)) {
			return protocol.TransformResult{}, protocolFailure(fmt.Errorf("Chat tool arguments are not valid JSON"))
		}
		typeName := tool.typeName
		if typeName == "" {
			typeName = "function"
		}
		c.message.ToolCalls = append(c.message.ToolCalls, chat.ToolCall{
			ID: tool.id, Type: typeName,
			Function: chat.FunctionCall{Name: tool.name, Arguments: arguments},
		})
	}
	if c.message.Role == "" {
		c.message.Role = "assistant"
	}
	if model := c.exchange.OriginalRequest.Model; model != "" {
		c.response.Model = model
	}
	c.response.Choices = []chat.Choice{{Index: 0, Message: c.message, FinishReason: c.finish}}
	body, err := chat.Encode(c.response)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	return protocol.TransformResult{Body: body}, nil
}

type responsesCollector struct {
	exchange protocol.Exchange
	response *responses.Response
	terminal bool
}

func (c *responsesCollector) Push(ctx context.Context, frame protocol.SSEFrame) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.terminal {
		return protocolFailure(fmt.Errorf("Responses collector received frame after terminal event"))
	}
	event := frame.Event
	if event == "" {
		event = dataType(frame.Data)
	}
	switch event {
	case "response.completed":
		var payload struct {
			Response responses.Response `json:"response"`
		}
		if err := json.Unmarshal(frame.Data, &payload); err != nil {
			return protocolFailure(fmt.Errorf("decode response.completed: %w", err))
		}
		c.response = &payload.Response
		c.terminal = true
	case "response.failed", "error":
		return protocolFailure(fmt.Errorf("Responses stream failed"))
	case "":
		return nil
	default:
		if !json.Valid(frame.Data) {
			return protocolFailure(fmt.Errorf("invalid Responses event JSON"))
		}
	}
	return nil
}

func (c *responsesCollector) Close(ctx context.Context) (protocol.TransformResult, error) {
	if err := ctx.Err(); err != nil {
		return protocol.TransformResult{}, err
	}
	if !c.terminal || c.response == nil {
		return protocol.TransformResult{}, protocolFailure(fmt.Errorf("Responses stream ended before response.completed"))
	}
	if model := c.exchange.OriginalRequest.Model; model != "" {
		c.response.Model = model
	}
	body, err := responses.Encode(c.response)
	if err != nil {
		return protocol.TransformResult{}, protocolFailure(err)
	}
	return protocol.TransformResult{Body: body}, nil
}
