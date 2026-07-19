package codex

// SSE parser for the OpenAI Codex /codex/responses stream.
//
// The wire shape we read is the OpenAI Responses SSE vocabulary
// (https://platform.openai.com/docs/api-reference/responses-streaming).
// We only care about three event types:
//
//   - response.output_text.delta  — incremental text content; the
//                                   `delta` field carries the new
//                                   characters to append to the
//                                   assistant message.
//   - response.output_item.done   — terminal marker for one output
//                                   item (message OR tool_call). We
//                                   forward tool_call items as
//                                   PART_KIND_TOOL_USE chunks; text
//                                   items are skipped (their text was
//                                   already streamed as deltas).
//   - response.completed          — stream-end sentinel; we emit
//                                   Done=true and stop reading.
//
// Unknown event types are skipped (not failed). The terminal chunk
// carries Done=true; the runtime folds the chunk stream into a
// ModelResult.
//
// Two additional sentinel events are honored:
//
//   - "data: [DONE]"              — OpenAI's older sentinel; emit
//                                   Done=true and return.
//   - "type": "error"             — surface as terminal chunk with
//                                   no payload; the runtime treats it
//                                   as a normal end-of-stream.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bizshuk/agentsdk/core"
)

// ParseStream reads SSE from r and feeds core.ModelChunk events into
// the returned channel. The terminal chunk carries Done=true.
//
// The function launches a goroutine that owns the channel; callers
// read until the channel closes. The goroutine honors ctx.Done()
// between events so a cancelled context stops the drain.
func ParseStream(ctx context.Context, r io.Reader) (<-chan core.ModelChunk, error) {
	if r == nil {
		return nil, nil
	}
	out := make(chan core.ModelChunk, 16)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(r)
		// Tool call arguments can be large for complex JSON schemas.
		// 1 MiB is a safe upper bound for one SSE event line.
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				emitDone(ctx, out)
				return
			}
			var chunk StreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				continue // skip malformed lines
			}
			switch chunk.Type {
			case "response.output_text.delta":
				if chunk.Delta != "" {
					select {
					case out <- core.ModelChunk{Kind: core.PART_KIND_PLAIN_TEXT, Text: chunk.Delta}:
					case <-ctx.Done():
						return
					}
				}
			case "response.output_item.done":
				if chunk.Item != nil && chunk.Item.Type == "tool_call" {
					select {
					case out <- core.ModelChunk{
						Kind: core.PART_KIND_TOOL_USE,
						ToolUse: &core.ToolUseChunk{
							ID:   chunk.Item.ID,
							Name: chunk.Item.Name,
							Args: decodeArgs(chunk.Item.Arguments),
						},
					}:
					case <-ctx.Done():
						return
					}
				}
			case "response.completed":
				emitDone(ctx, out)
				return
			case "error":
				// Surface as terminal chunk; the runtime treats
				// any stream-end as the end of the response.
				emitDone(ctx, out)
				return
			}
		}

		// EOF without an explicit terminal event — make sure the
		// caller sees Done=true so they fold the stream.
		emitDone(ctx, out)
	}()
	return out, nil
}

// emitDone sends the terminal sentinel chunk. It uses a non-blocking
// send on a buffered channel; if the consumer has already given up
// we drop the chunk rather than block forever.
func emitDone(ctx context.Context, out chan<- core.ModelChunk) {
	select {
	case out <- core.ModelChunk{Done: true}:
	case <-ctx.Done():
	}
}

// decodeArgs best-effort parses a tool-call arguments blob. Returns
// nil on failure so the caller can decide what to do.
func decodeArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}
