// SSE parser for the xAI Grok chat-completions stream. The wire shape is
// the same as OpenAI's — each event is a `data: <json>` line where the
// payload matches StreamChunk in dto.go. End of stream is `data: [DONE]`.

package grok

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/bizshuk/agentsdk/core"
)

// ParseStream reads SSE from r and feeds core.ModelChunk events into the
// returned channel. The terminal chunk carries Done=true; the runtime
// folds those into ModelResult.
//
// Unknown lines and malformed JSON are skipped, not failed. This matches
// the behavior of pi's openai-compat stream parser and keeps a single
// dropped delta from killing an otherwise healthy stream.
func ParseStream(ctx context.Context, r io.Reader) <-chan core.ModelChunk {
	out := make(chan core.ModelChunk, 16)

	go func() {
		defer close(out)
		scanner := bufio.NewScanner(r)
		// Tool-call argument deltas can grow large for complex schemas;
		// 1 MiB is a safe upper bound that matches the ollama
		// adapter's buffer strategy.
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				if payload == "[DONE]" {
					break
				}
				continue
			}
			var chunk StreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
				continue
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta
			if delta.Content != "" {
				select {
				case out <- core.ModelChunk{Kind: core.PART_KIND_PLAIN_TEXT, Text: delta.Content}:
				case <-ctx.Done():
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

	return out
}

