package anthropic

// SSE parser for the Anthropic /v1/messages stream. The wire shape we
// read is documented at https://docs.anthropic.com/en/docs/build-with-claude/streaming:
//
//	event: message_start
//	data: {"type":"message_start","message":{...}}
//
//	event: content_block_start
//	data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
//
//	event: content_block_delta
//	data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}
//
//	event: content_block_stop
//	data: {"type":"content_block_stop","index":0}
//
//	event: message_delta
//	data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":N}}
//
//	event: message_stop
//	data: {"type":"message_stop"}
//
//	event: ping
//	data: {"type":"ping"}
//
//	event: error
//	data: {"type":"error","error":{"type":"...","message":"..."}}
//
// We ignore `event:` lines — `data:` carries everything we need via JSON's
// `type` field. Unknown events are skipped, not failed.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bizshuk/agentsdk/core"
)

// StreamEvent is one SSE event from the Anthropic stream. The fields are
// intentionally optional — different events populate different subsets.
type StreamEvent struct {
	Type         string        `json:"type"`
	Index        int           `json:"index,omitempty"`
	ContentBlock *ContentBlock `json:"content_block,omitempty"`
	Delta        *StreamDelta  `json:"delta,omitempty"`
	Message      *Response     `json:"message,omitempty"`
	Usage        *Usage        `json:"usage,omitempty"`
	Error        *StreamError  `json:"error,omitempty"`
}

// StreamDelta is the per-event delta payload. Its `type` distinguishes
// text_delta from input_json_delta (the latter carries partial tool args).
type StreamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// StreamError is the error event payload.
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// StreamStop is the terminal event emitted on the chunk channel. It carries
// the final stop_reason and usage that the runtime folds into ModelResult.
type StreamStop struct {
	StopReason string
	Usage      Usage
}

// ParseStream reads SSE from r and feeds core.ModelChunk events into the
// returned channel. The terminal chunk carries Done=true with optional
// usage/stopReason — the runtime folds those into ModelResult.
//
// Returns the chunk channel and a function to retrieve the terminal
// metadata once the channel is drained.
func ParseStream(ctx context.Context, r io.Reader) (<-chan core.ModelChunk, *StreamStop) {
	out := make(chan core.ModelChunk, 16)
	stop := &StreamStop{}

	go func() {
		defer close(out)
		scanner := bufio.NewScanner(r)
		// Anthropic delta lines are small, but tool input_json_delta can
		// grow large for complex schemas. 1 MiB is a safe upper bound.
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var ev StreamEvent
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue // ignore malformed lines
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta != nil && ev.Delta.Text != "" {
					select {
					case out <- core.ModelChunk{Kind: core.PART_KIND_PLAIN_TEXT, Text: ev.Delta.Text}:
					case <-ctx.Done():
						return
					}
				}
			case "content_block_stop":
				if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
					select {
					case out <- core.ModelChunk{
						Kind: core.PART_KIND_TOOL_USE,
						ToolUse: &core.ToolUseChunk{
							ID:   ev.ContentBlock.ID,
							Name: ev.ContentBlock.Name,
							Args: decodeArgs(ev.ContentBlock.Input),
						},
					}:
					case <-ctx.Done():
						return
					}
				}
			case "message_delta":
				if ev.Delta != nil && ev.Delta.StopReason != "" {
					stop.StopReason = ev.Delta.StopReason
				}
				if ev.Usage != nil {
					stop.Usage = *ev.Usage
				}
			case "error":
				if ev.Error != nil {
					select {
					case out <- core.ModelChunk{
						Kind: core.PART_KIND_PLAIN_TEXT,
						Text: fmt.Sprintf("[anthropic error: %s]", ev.Error.Message),
						Done: true,
					}:
					case <-ctx.Done():
						return
					}
					return
				}
			}
		}

		// Terminal sentinel.
		select {
		case out <- core.ModelChunk{Kind: core.PART_KIND_PLAIN_TEXT, Done: true}:
		case <-ctx.Done():
		}
	}()

	return out, stop
}

func decodeArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}
