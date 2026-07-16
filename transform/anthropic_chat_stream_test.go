package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func task8Exchange(originalModel, translatedModel string) protocol.Exchange {
	next := 0
	return protocol.Exchange{
		OriginalRequest:   protocol.RequestEnvelope{Model: originalModel},
		TranslatedRequest: protocol.RequestEnvelope{Model: translatedModel},
		ProviderID:        "test-provider",
		NewID: func() string {
			next++
			return fmt.Sprintf("generated_%d", next)
		},
	}
}

func task8FullChatFrames(callID string) []protocol.SSEFrame {
	return []protocol.SSEFrame{
		{Data: []byte(`{"id":"chat_1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"check"}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"done"}}]}`)},
		{Data: []byte(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`, callID))},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)},
		{Data: []byte(`[DONE]`)},
	}
}

func task8PushAll(t *testing.T, stream StreamTransform, input []protocol.SSEFrame) []protocol.SSEFrame {
	t.Helper()
	var output []protocol.SSEFrame
	for _, frame := range input {
		frames, err := stream.Push(context.Background(), frame)
		require.NoError(t, err)
		output = append(output, frames...)
	}
	return output
}

func task8Close(t *testing.T, stream StreamTransform, output *[]protocol.SSEFrame) error {
	t.Helper()
	frames, err := stream.Close(context.Background())
	if output != nil {
		*output = append(*output, frames...)
	}
	return err
}

func task8EventNames(frames []protocol.SSEFrame) []string {
	names := make([]string, 0, len(frames))
	for _, frame := range frames {
		names = append(names, frame.Event)
	}
	return names
}

func task8DecodeData(t *testing.T, frame protocol.SSEFrame) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(frame.Data, &payload))
	return payload
}

func TestChatToAnthropicStreamLifecycle(t *testing.T) {
	stream, err := NewChatToAnthropicStream(task8Exchange("claude-3", "gpt-4o"))
	require.NoError(t, err)

	output := task8PushAll(t, stream, task8FullChatFrames("call_1"))
	require.NoError(t, task8Close(t, stream, &output))
	assert.Equal(t, []string{
		"message_start", "content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}, task8EventNames(output))

	messageStart := task8DecodeData(t, output[0])
	message := messageStart["message"].(map[string]any)
	assert.Equal(t, "chat_1", message["id"])
	assert.Equal(t, "claude-3", message["model"])

	toolStart := task8DecodeData(t, output[7])
	toolBlock := toolStart["content_block"].(map[string]any)
	assert.Equal(t, "call_1", toolBlock["id"])
	assert.Equal(t, "read", toolBlock["name"])

	messageDelta := task8DecodeData(t, output[11])
	delta := messageDelta["delta"].(map[string]any)
	usage := messageDelta["usage"].(map[string]any)
	assert.Equal(t, "tool_use", delta["stop_reason"])
	assert.EqualValues(t, 4, usage["output_tokens"])
}

func task8FullAnthropicFrames(callID string) []protocol.SSEFrame {
	return []protocol.SSEFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"check"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":1}`)},
		{Event: "content_block_start", Data: []byte(fmt.Sprintf(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":%q,"name":"read","input":{}}}`, callID))},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":"}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"\"a.txt\"}"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":2}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":4}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
}

func TestAnthropicToChatStreamLifecycle(t *testing.T) {
	stream, err := NewAnthropicToChatStream(task8Exchange("gpt-4o", "claude-3"))
	require.NoError(t, err)

	output := task8PushAll(t, stream, task8FullAnthropicFrames("call_1"))
	require.NoError(t, task8Close(t, stream, &output))
	require.Len(t, output, 8)
	for _, frame := range output {
		assert.Empty(t, frame.Event)
	}

	primer := task8DecodeData(t, output[0])
	assert.Equal(t, "msg_1", primer["id"])
	assert.Equal(t, "gpt-4o", primer["model"])
	primerChoice := primer["choices"].([]any)[0].(map[string]any)
	primerDelta := primerChoice["delta"].(map[string]any)
	assert.Equal(t, "assistant", primerDelta["role"])

	reasoningChoice := task8DecodeData(t, output[1])["choices"].([]any)[0].(map[string]any)
	assert.Equal(t, "check", reasoningChoice["delta"].(map[string]any)["reasoning_content"])
	textChoice := task8DecodeData(t, output[2])["choices"].([]any)[0].(map[string]any)
	assert.Equal(t, "done", textChoice["delta"].(map[string]any)["content"])

	toolStartChoice := task8DecodeData(t, output[3])["choices"].([]any)[0].(map[string]any)
	toolStart := toolStartChoice["delta"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	assert.EqualValues(t, 0, toolStart["index"])
	assert.Equal(t, "call_1", toolStart["id"])
	assert.Equal(t, "read", toolStart["function"].(map[string]any)["name"])

	finish := task8DecodeData(t, output[6])
	finishChoice := finish["choices"].([]any)[0].(map[string]any)
	assert.Equal(t, "tool_calls", finishChoice["finish_reason"])
	usage := finish["usage"].(map[string]any)
	assert.EqualValues(t, 10, usage["prompt_tokens"])
	assert.EqualValues(t, 4, usage["completion_tokens"])
	assert.Equal(t, "[DONE]", string(output[7].Data))
}

func TestChatToAnthropicStreamCloseRejectsMissingTerminal(t *testing.T) {
	stream, err := NewChatToAnthropicStream(task8Exchange("claude", "gpt"))
	require.NoError(t, err)
	_, err = stream.Push(context.Background(), protocol.SSEFrame{
		Data: []byte(`{"choices":[{"index":0,"delta":{"content":"partial"}}]}`),
	})
	require.NoError(t, err)

	err = task8Close(t, stream, nil)
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestChatToAnthropicStreamRejectsDoneWithoutFinishReason(t *testing.T) {
	stream, err := NewChatToAnthropicStream(task8Exchange("claude", "gpt"))
	require.NoError(t, err)

	_, err = stream.Push(context.Background(), protocol.SSEFrame{Data: []byte(`[DONE]`)})
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestAnthropicToChatStreamErrorTerminatesInChatShape(t *testing.T) {
	stream, err := NewAnthropicToChatStream(task8Exchange("gpt", "claude"))
	require.NoError(t, err)

	output, err := stream.Push(context.Background(), protocol.SSEFrame{
		Event: "error",
		Data:  []byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`),
	})
	require.NoError(t, err)
	require.Len(t, output, 2)
	assert.JSONEq(t, `{"error":{"type":"overloaded_error","message":"busy"}}`, string(output[0].Data))
	assert.Equal(t, "[DONE]", string(output[1].Data))
	assert.NoError(t, task8Close(t, stream, nil))
}

func TestChatToAnthropicStreamErrorTerminatesInAnthropicShape(t *testing.T) {
	stream, err := NewChatToAnthropicStream(task8Exchange("claude", "gpt"))
	require.NoError(t, err)

	output, err := stream.Push(context.Background(), protocol.SSEFrame{
		Data: []byte(`{"error":{"type":"server_error","code":"upstream_error","message":"busy"}}`),
	})
	require.NoError(t, err)
	require.Len(t, output, 1)
	assert.Equal(t, "error", output[0].Event)
	assert.JSONEq(t, `{"type":"error","error":{"type":"server_error","code":"upstream_error","message":"busy"}}`, string(output[0].Data))
	assert.NoError(t, task8Close(t, stream, nil))
}

func TestAnthropicChatStreamsKeepConcurrentToolStateIsolated(t *testing.T) {
	type result struct {
		callID string
		body   string
		err    error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for _, callID := range []string{"call_alpha", "call_beta"} {
		callID := callID
		wait.Add(1)
		go func() {
			defer wait.Done()
			stream, err := NewChatToAnthropicStream(task8Exchange("claude", "gpt"))
			if err != nil {
				results <- result{callID: callID, err: err}
				return
			}
			var body strings.Builder
			for _, frame := range task8FullChatFrames(callID) {
				output, pushErr := stream.Push(context.Background(), frame)
				if pushErr != nil {
					results <- result{callID: callID, err: pushErr}
					return
				}
				for _, outputFrame := range output {
					body.Write(outputFrame.Data)
				}
			}
			_, err = stream.Close(context.Background())
			results <- result{callID: callID, body: body.String(), err: err}
		}()
	}
	wait.Wait()
	close(results)

	for got := range results {
		require.NoError(t, got.err)
		assert.Contains(t, got.body, got.callID)
		otherID := "call_alpha"
		if got.callID == otherID {
			otherID = "call_beta"
		}
		assert.NotContains(t, got.body, otherID)
	}
}

func TestAnthropicChatStreamsRejectInvalidAccumulatedToolJSON(t *testing.T) {
	tests := []struct {
		name string
		new  func(protocol.Exchange) (StreamTransform, error)
		in   []protocol.SSEFrame
	}{
		{
			name: "Chat to Anthropic",
			new:  NewChatToAnthropicStream,
			in: []protocol.SSEFrame{
				{Data: []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_bad","type":"function","function":{"name":"read","arguments":"{"}}]}}]}`)},
				{Data: []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)},
			},
		},
		{
			name: "Anthropic to Chat",
			new:  NewAnthropicToChatStream,
			in: []protocol.SSEFrame{
				{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_bad","model":"claude","usage":{"input_tokens":1,"output_tokens":0}}}`)},
				{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_bad","name":"read","input":{}}}`)},
				{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{"}}`)},
				{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := tc.new(task8Exchange("client-model", "provider-model"))
			require.NoError(t, err)
			for index, frame := range tc.in {
				_, err = stream.Push(context.Background(), frame)
				if index < len(tc.in)-1 {
					require.NoError(t, err)
				}
			}
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, protocol.ERROR_PROTOCOL, proxyErr.Kind)
		})
	}
}

func TestChatToAnthropicStreamSerializesParallelToolBlocks(t *testing.T) {
	stream, err := NewChatToAnthropicStream(task8Exchange("claude", "gpt"))
	require.NoError(t, err)
	input := []protocol.SSEFrame{
		{Data: []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_0","type":"function","function":{"name":"read","arguments":"{\"path\":\"a\"}"}},{"index":1,"id":"call_1","type":"function","function":{"name":"write","arguments":"{\"path\":\"b\"}"}}]}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)},
		{Data: []byte(`[DONE]`)},
	}

	output := task8PushAll(t, stream, input)
	require.NoError(t, task8Close(t, stream, &output))
	assert.Equal(t, []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}, task8EventNames(output))
	assert.EqualValues(t, 0, task8DecodeData(t, output[1])["index"])
	assert.EqualValues(t, 0, task8DecodeData(t, output[3])["index"])
	assert.EqualValues(t, 1, task8DecodeData(t, output[4])["index"])
	assert.EqualValues(t, 1, task8DecodeData(t, output[6])["index"])
}
