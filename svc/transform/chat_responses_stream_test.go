package transform

import (
	"context"
	"fmt"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatToResponsesStreamIsIncrementalAndCollectable(t *testing.T) {
	stream, err := NewChatToResponsesStream(streamTestExchange("gpt-chat", "gpt-responses"))
	require.NoError(t, err)
	var output []model.SSEFrame
	for index, frame := range collectorChatFrames() {
		frames, pushErr := stream.Push(context.Background(), frame)
		require.NoError(t, pushErr)
		if index == 1 {
			assert.Contains(t, streamEventNames(frames), "response.reasoning_summary_text.delta")
		}
		if index == 2 {
			assert.Contains(t, streamEventNames(frames), "response.output_text.delta")
		}
		output = append(output, frames...)
	}
	closed, err := stream.Close(context.Background())
	require.NoError(t, err)
	output = append(output, closed...)
	assert.Contains(t, streamEventNames(output), "response.completed")

	collector, err := NewStreamCollector(model.FORMAT_OPENAI_RESPONSES, model.Exchange{})
	require.NoError(t, err)
	for _, frame := range output {
		require.NoError(t, collector.Push(context.Background(), frame))
	}
	result, err := collector.Close(context.Background())
	require.NoError(t, err)
	assertResponseSemantics(t, model.FORMAT_OPENAI_RESPONSES, result.Body, responseSemantics{
		text: "done", reasoning: "checked", toolName: "read", callID: "call_1",
		inputTokens: 10, outputTokens: 4, stop: "tool",
	})
}

func TestResponsesToChatStreamIsIncrementalAndCollectable(t *testing.T) {
	stream, err := NewResponsesToChatStream(streamTestExchange("gpt-responses", "gpt-chat"))
	require.NoError(t, err)
	var output []model.SSEFrame
	for _, frame := range chatResponsesFullResponsesFrames() {
		frames, pushErr := stream.Push(context.Background(), frame)
		require.NoError(t, pushErr)
		output = append(output, frames...)
	}
	closed, err := stream.Close(context.Background())
	require.NoError(t, err)
	output = append(output, closed...)
	require.NotEmpty(t, output)
	assert.Equal(t, "[DONE]", string(output[len(output)-1].Data))

	collector, err := NewStreamCollector(model.FORMAT_OPENAI_CHAT, model.Exchange{})
	require.NoError(t, err)
	for _, frame := range output {
		require.NoError(t, collector.Push(context.Background(), frame))
	}
	result, err := collector.Close(context.Background())
	require.NoError(t, err)
	assertResponseSemantics(t, model.FORMAT_OPENAI_CHAT, result.Body, responseSemantics{
		text: "done", reasoning: "checked", toolName: "read", callID: "call_1",
		inputTokens: 10, outputTokens: 4, stop: "tool",
	})
}

func TestChatResponsesStreamCloseRejectsMissingTerminal(t *testing.T) {
	stream, err := NewChatToResponsesStream(streamTestExchange("gpt", "gpt"))
	require.NoError(t, err)
	_, err = stream.Push(context.Background(), collectorChatFrames()[0])
	require.NoError(t, err)
	_, err = stream.Close(context.Background())
	require.Error(t, err)
}

func streamTestExchange(originalModel, translatedModel string) model.Exchange {
	next := 0
	return model.Exchange{
		OriginalRequest:   model.RequestEnvelope{Model: originalModel},
		TranslatedRequest: model.RequestEnvelope{Model: translatedModel},
		NewID: func() string {
			next++
			return fmt.Sprintf("generated_%d", next)
		},
	}
}

func streamEventNames(frames []model.SSEFrame) []string {
	names := make([]string, 0, len(frames))
	for _, frame := range frames {
		names = append(names, frame.Event)
	}
	return names
}

func chatResponsesFullResponsesFrames() []model.SSEFrame {
	return []model.SSEFrame{
		{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-responses","status":"in_progress"}}`)},
		{Event: "response.reasoning_summary_text.delta", Data: []byte(`{"type":"response.reasoning_summary_text.delta","delta":"checked"}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`)},
		{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"done"}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`)},
		{Event: "response.function_call_arguments.delta", Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":1,"delta":"{\"path\":\"a.txt\"}"}`)},
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-responses","status":"completed","output":[{"id":"reason_1","type":"reasoning","summary":[{"type":"summary_text","text":"checked"}]},{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]},{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"}],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14}}}`)},
	}
}
