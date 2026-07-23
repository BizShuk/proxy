package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamCollectorsBuildEquivalentNonStreamResponses(t *testing.T) {
	tests := []struct {
		name   string
		format model.Format
		frames []model.SSEFrame
	}{
		{name: "anthropic", format: model.FORMAT_ANTHROPIC_MESSAGES, frames: collectorAnthropicFrames()},
		{name: "chat", format: model.FORMAT_OPENAI_CHAT, frames: collectorChatFrames()},
		{name: "responses", format: model.FORMAT_OPENAI_RESPONSES, frames: collectorResponsesFrames()},
	}
	want := responseSemantics{
		text: "done", reasoning: "checked", toolName: "read", callID: "call_1",
		inputTokens: 10, outputTokens: 4, stop: "tool",
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			collector, err := NewStreamCollector(tc.format, model.Exchange{
				OriginalRequest: model.RequestEnvelope{Model: "client-model"},
			})
			require.NoError(t, err)
			for _, frame := range tc.frames {
				require.NoError(t, collector.Push(context.Background(), frame))
			}
			result, err := collector.Close(context.Background())
			require.NoError(t, err)
			assertResponseSemantics(t, tc.format, result.Body, want)
		})
	}
}

func TestStreamCollectorRejectsMissingAndDuplicateTerminal(t *testing.T) {
	collector, err := NewStreamCollector(model.FORMAT_OPENAI_CHAT, model.Exchange{})
	require.NoError(t, err)
	require.NoError(t, collector.Push(context.Background(), collectorChatFrames()[0]))
	_, err = collector.Close(context.Background())
	require.Error(t, err)

	collector, err = NewStreamCollector(model.FORMAT_OPENAI_RESPONSES, model.Exchange{})
	require.NoError(t, err)
	frame := collectorResponsesFrames()[0]
	require.NoError(t, collector.Push(context.Background(), frame))
	err = collector.Push(context.Background(), frame)
	require.Error(t, err)
}

func TestStreamCollectorRejectsFailureEvent(t *testing.T) {
	collector, err := NewStreamCollector(model.FORMAT_OPENAI_RESPONSES, model.Exchange{})
	require.NoError(t, err)
	err = collector.Push(context.Background(), model.SSEFrame{
		Event: "response.failed",
		Data:  []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"failed"}}}`),
	})
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestAnthropicCollectorPushErrorFrame(t *testing.T) {
	t.Run("captures error details", func(t *testing.T) {
		collector, err := NewStreamCollector(model.FORMAT_ANTHROPIC_MESSAGES, model.Exchange{
			OriginalRequest: model.RequestEnvelope{Model: "openai/gpt-5.5"},
		})
		require.NoError(t, err)
		err = collector.Push(context.Background(), model.SSEFrame{
			Event: "error",
			Data: []byte(`{"type":"error","error":{"type":"upstream_error","message":"boom","code":"context_overflow"}}`),
		})
		var proxyErr *model.ProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, model.ERROR_UPSTREAM, proxyErr.Kind)
		assert.Equal(t, "context_overflow", proxyErr.Code)
		assert.Equal(t, "context_overflow", proxyErr.UpstreamErrorCode)
		assert.Equal(t, "boom", proxyErr.UpstreamErrorMessage)
		assert.Equal(t, "upstream_error", proxyErr.UpstreamErrorType)
	})
}

func collectorAnthropicFrames() []model.SSEFrame {
	return []model.SSEFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],"usage":{"input_tokens":10,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"checked"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":1}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"read","input":{}}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":2}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
}

func collectorChatFrames() []model.SSEFrame {
	return []model.SSEFrame{
		{Data: []byte(`{"id":"chat_1","model":"gpt","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)},
		{Data: []byte(`{"id":"chat_1","choices":[{"index":0,"delta":{"reasoning_content":"checked"}}]}`)},
		{Data: []byte(`{"id":"chat_1","choices":[{"index":0,"delta":{"content":"done"}}]}`)},
		{Data: []byte(`{"id":"chat_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`)},
		{Data: []byte(`{"id":"chat_1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)},
		{Data: []byte(`[DONE]`)},
	}
}

func collectorResponsesFrames() []model.SSEFrame {
	return []model.SSEFrame{{
		Event: "response.completed",
		Data:  []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt","status":"completed","output":[{"id":"reason_1","type":"reasoning","summary":[{"type":"summary_text","text":"checked"}]},{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`),
	}}
}
