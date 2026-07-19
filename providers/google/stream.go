// SSE parser for the OpenAI-compatible /chat/completions stream.
// The wire shape we read is documented at
// https://platform.openai.com/docs/api-reference/chat-streaming:
//
//	data: {"id":"chatcmpl-...","choices":[{"delta":{"content":"hi"}}]}
//	data: {"choices":[{"finish_reason":"stop"}]}
//	data: [DONE]
//
// We ignore everything except the `data:` lines; each one is a JSON
// object we unmarshal as StreamChunk and project into core.ModelChunk.

package google

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bizshuk/agentsdk/core"
)

// ParseStream reads SSE from r and yields core.ModelChunk events on
// the returned channel. The terminal chunk carries Done=true. Lines
// that fail to parse are skipped, not failed — partial frames mid-stream
// are common and recoverable.
//
// The returned channel is closed when the stream is exhausted or the
// context is canceled.
func ParseStream(ctx context.Context, r io.Reader) (<-chan core.ModelChunk, error) {
	out := make(chan core.ModelChunk, 16)
	go func() {
		defer close(out)
		scanner := bufio.NewScanner(r)
		// 1 MiB cap handles large tool-call argument deltas.
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
				select {
				case out <- core.ModelChunk{Done: true}:
				case <-ctx.Done():
					return
				}
				continue
			}
			var chunk StreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				continue // skip malformed frames
			}
			for _, c := range chunk.Choices {
				if c.Delta.Content != "" {
					select {
					case out <- core.ModelChunk{Kind: core.PART_KIND_PLAIN_TEXT, Text: c.Delta.Content}:
					case <-ctx.Done():
						return
					}
				}
				for _, tc := range c.Delta.ToolCalls {
					select {
					case out <- core.ModelChunk{
						Kind: core.PART_KIND_TOOL_USE,
						ToolUse: &core.ToolUseChunk{
							ID:   tc.ID,
							Name: tc.Function.Name,
							Args: parseArgs(tc.Function.Arguments),
						},
					}:
					case <-ctx.Done():
						return
					}
				}
			}
		}

		// Always emit a terminal chunk so downstream folders don't hang.
		select {
		case out <- core.ModelChunk{Done: true}:
		case <-ctx.Done():
		}
	}()
	return out, nil
}

// parseArgs decodes the tool-call Arguments string into a generic map.
// OpenAI wire format passes arguments as a JSON-encoded string; most
// servers honor that, some send a nested object — both shapes end up
// as map[string]any here.
func parseArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	return m
}