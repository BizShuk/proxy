package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runAntigravityStream feeds provider frames through the transform and returns
// the client-facing event names and payloads in order.
func runAntigravityStream(t *testing.T, frames ...string) ([]string, []string) {
	t.Helper()
	stream, err := NewAntigravityToAnthropicStream(model.Exchange{
		OriginalRequest: model.RequestEnvelope{Model: "gemini-3.1-pro-high"},
		NewID:           func() string { return "msg_generated" },
	})
	require.NoError(t, err)

	var events, payloads []string
	for _, frame := range frames {
		out, err := stream.Push(context.Background(), model.SSEFrame{Data: []byte(frame)})
		require.NoError(t, err)
		for _, produced := range out {
			events = append(events, produced.Event)
			payloads = append(payloads, string(produced.Data))
		}
	}
	out, err := stream.Close(context.Background())
	require.NoError(t, err)
	for _, produced := range out {
		events = append(events, produced.Event)
		payloads = append(payloads, string(produced.Data))
	}
	return events, payloads
}

func TestAntigravityStreamEmitsAnthropicLifecycle(t *testing.T) {
	events, payloads := runAntigravityStream(t,
		`{"response":{"responseId":"resp_1","candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"modelVersion":"gemini-3.1-pro-high"}}`,
		`{"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}}}`,
	)

	assert.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, events)
	assert.Contains(t, payloads[0], `"id":"resp_1"`)
	assert.Contains(t, payloads[0], `"model":"gemini-3.1-pro-high"`)
	assert.Contains(t, payloads[2], `"text_delta"`)
	assert.Contains(t, payloads[4], `"stop_reason":"end_turn"`)
	assert.Contains(t, payloads[4], `"output_tokens":1`)
}

// Gemini delivers a complete argument object in one part, so the tool block
// opens, receives its whole payload, and closes without partial accumulation.
func TestAntigravityStreamEmitsCompleteToolBlock(t *testing.T) {
	events, payloads := runAntigravityStream(t,
		`{"response":{"responseId":"resp_2","candidates":[{"content":{"parts":[{"functionCall":{"id":"call_1","name":"read_file","args":{"path":"a.txt"}}}]}}]}}`,
		`{"response":{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}}`,
	)

	assert.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, events)
	assert.Contains(t, payloads[1], `"type":"tool_use"`)
	assert.Contains(t, payloads[1], `"name":"read_file"`)
	assert.Contains(t, payloads[2], `"input_json_delta"`)
	assert.Contains(t, payloads[2], `a.txt`)
	// The gateway still says STOP; the tool part is what makes it tool_use.
	assert.Contains(t, payloads[4], `"stop_reason":"tool_use"`)
}

// Anthropic requires signature_delta inside the thinking block it signs, so the
// signature must land before that block is closed.
func TestAntigravityStreamSignsThinkingBeforeClose(t *testing.T) {
	events, payloads := runAntigravityStream(t,
		`{"response":{"responseId":"resp_3","candidates":[{"content":{"parts":[{"thought":true,"text":"ponder","thoughtSignature":"sig-1"}]}}]}}`,
		`{"response":{"candidates":[{"content":{"parts":[{"text":"answer"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":3}}}`,
	)

	assert.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, events)
	assert.Contains(t, payloads[1], `"type":"thinking"`)
	assert.Contains(t, payloads[2], `"thinking_delta"`)
	assert.Contains(t, payloads[3], `"signature_delta"`)
	assert.Contains(t, payloads[3], `sig-1`)
	assert.Contains(t, payloads[5], `"type":"text"`)
}

// Finish reason and usage can arrive in separate frames; the terminal delta
// must wait for both so output_tokens is never reported as zero.
func TestAntigravityStreamWaitsForUsageBeforeTerminalDelta(t *testing.T) {
	events, payloads := runAntigravityStream(t,
		`{"response":{"responseId":"resp_4","candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}}`,
		`{"response":{"usageMetadata":{"promptTokenCount":9,"candidatesTokenCount":4}}}`,
	)

	assert.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, events)
	assert.Contains(t, payloads[4], `"output_tokens":4`)
	assert.Contains(t, payloads[4], `"input_tokens":9`)
}

// A stream that dies before any usage frame still has to terminate cleanly, or
// the Anthropic client hangs waiting for message_stop.
func TestAntigravityStreamClosesWithoutUsage(t *testing.T) {
	events, _ := runAntigravityStream(t,
		`{"response":{"responseId":"resp_5","candidates":[{"content":{"parts":[{"text":"partial"}]}}]}}`,
	)

	assert.Equal(t, []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}, events)
}

func TestAntigravityStreamPropagatesUpstreamError(t *testing.T) {
	stream, err := NewAntigravityToAnthropicStream(model.Exchange{})
	require.NoError(t, err)

	out, err := stream.Push(context.Background(), model.SSEFrame{
		Data: []byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"rate limited"}}`),
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "error", out[0].Event)
	assert.Contains(t, string(out[0].Data), "RESOURCE_EXHAUSTED")

	// The terminal error ends the stream: later frames are a protocol violation.
	_, err = stream.Push(context.Background(), model.SSEFrame{Data: []byte(`{"response":{}}`)})
	require.Error(t, err)
}

func TestAntigravityStreamRejectsEmptyStream(t *testing.T) {
	stream, err := NewAntigravityToAnthropicStream(model.Exchange{})
	require.NoError(t, err)

	_, err = stream.Close(context.Background())
	require.Error(t, err)
}
