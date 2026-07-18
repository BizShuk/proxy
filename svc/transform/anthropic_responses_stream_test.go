package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropicStreamLifecycle(t *testing.T) {
	stream, err := NewResponsesToAnthropicStream(task9ExchangeFor("claude-3", "gpt-5"))
	require.NoError(t, err)

	output := task9PushAll(t, stream, task9FullResponsesFrames())
	require.NoError(t, task9CloseStream(t, stream, &output))

	assert.Equal(t, []string{
		"message_start",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}, task9EventNames(output))

	var messageStart struct {
		Message anthropic.Response `json:"message"`
	}
	task9DecodeEvent(t, output[0], &messageStart)
	assert.Equal(t, "resp_1", messageStart.Message.ID)
	assert.Equal(t, "claude-3", messageStart.Message.Model)

	var toolStart struct {
		Index        int               `json:"index"`
		ContentBlock anthropic.Content `json:"content_block"`
	}
	task9DecodeEvent(t, output[7], &toolStart)
	assert.Equal(t, 2, toolStart.Index)
	assert.Equal(t, "call_1", toolStart.ContentBlock.ID)
	assert.Equal(t, "read", toolStart.ContentBlock.Name)

	var argumentDelta struct {
		Delta struct {
			Type        string `json:"type"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	task9DecodeEvent(t, output[8], &argumentDelta)
	assert.Equal(t, "input_json_delta", argumentDelta.Delta.Type)
	assert.Equal(t, `{"path":"a.txt"}`, argumentDelta.Delta.PartialJSON)
	assert.True(t, json.Valid([]byte(argumentDelta.Delta.PartialJSON)))

	var messageDelta struct {
		Delta struct {
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage anthropic.Usage `json:"usage"`
	}
	task9DecodeEvent(t, output[10], &messageDelta)
	assert.Equal(t, "tool_use", messageDelta.Delta.StopReason)
	assert.Equal(t, 10, messageDelta.Usage.InputTokens)
	assert.Equal(t, 4, messageDelta.Usage.OutputTokens)
}

func TestAnthropicToResponsesStreamLifecycle(t *testing.T) {
	stream, err := NewAnthropicToResponsesStream(task9ExchangeFor("gpt-5", "claude-3"))
	require.NoError(t, err)

	output := task9PushAll(t, stream, task9FullAnthropicFrames())
	require.NoError(t, task9CloseStream(t, stream, &output))

	assert.Equal(t, []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.reasoning_summary_part.added",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done",
		"response.reasoning_summary_part.done", "response.output_item.done",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.done",
		"response.content_part.done", "response.output_item.done",
		"response.output_item.added", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}, task9EventNames(output))

	completed := task9CompletedResponse(t, output)
	assert.Equal(t, "msg_1", completed.ID)
	assert.Equal(t, "gpt-5", completed.Model)
	assert.Equal(t, "completed", completed.Status)
	require.NotNil(t, completed.Usage)
	assert.Equal(t, 10, completed.Usage.InputTokens)
	assert.Equal(t, 4, completed.Usage.OutputTokens)
	require.Len(t, completed.Output, 3)
	assert.Equal(t, "check", completed.Output[0].Summary[0].Text)
	assert.Equal(t, "done", completed.Output[1].Content[0].Text)
	assert.Equal(t, "call_1", completed.Output[2].CallID)
	assert.Equal(t, `{"path":"a.txt"}`, completed.Output[2].Arguments)
}

func TestAnthropicToResponsesStreamMatchesNonStreamSemantics(t *testing.T) {
	stream, err := NewAnthropicToResponsesStream(task9ExchangeFor("gpt-5", "claude-3"))
	require.NoError(t, err)
	output := task9PushAll(t, stream, task9FullAnthropicFrames())
	require.NoError(t, task9CloseStream(t, stream, &output))
	streamed := task9CompletedResponse(t, output)

	nonStreamExchange := task9ExchangeFor("gpt-5", "claude-3")
	nonStream, err := AnthropicToResponsesResponse(context.Background(), model.ResponseEnvelope{
		Body: []byte(`{
			"id":"msg_1","type":"message","role":"assistant","model":"claude-3",
			"content":[
				{"type":"thinking","thinking":"check"},
				{"type":"text","text":"done"},
				{"type":"tool_use","id":"call_1","name":"read","input":{"path":"a.txt"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":10,"output_tokens":4}
		}`),
		Exchange: nonStreamExchange,
	})
	require.NoError(t, err)
	want, err := responses.DecodeResponse(nonStream.Body)
	require.NoError(t, err)

	assert.Equal(t, want.Model, streamed.Model)
	assert.Equal(t, want.Status, streamed.Status)
	assert.Equal(t, want.Output, streamed.Output)
	assert.Equal(t, want.Usage, streamed.Usage)
}

func TestResponsesToAnthropicStreamFailedEvent(t *testing.T) {
	stream, err := NewResponsesToAnthropicStream(task9ExchangeFor("claude-3", "gpt-5"))
	require.NoError(t, err)

	output := task9PushAll(t, stream, []model.SSEFrame{{
		Event: "response.failed",
		Data:  []byte(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"upstream broke"}}}`),
	}})
	require.NoError(t, task9CloseStream(t, stream, &output))

	assert.Equal(t, []string{"error"}, task9EventNames(output))
	assert.NotContains(t, task9EventNames(output), "message_stop")
	var event struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	task9DecodeEvent(t, output[0], &event)
	assert.Equal(t, "upstream_error", event.Error.Type)
	assert.Equal(t, "upstream broke", event.Error.Message)
}

func TestAnthropicToResponsesStreamErrorEvent(t *testing.T) {
	stream, err := NewAnthropicToResponsesStream(task9ExchangeFor("gpt-5", "claude-3"))
	require.NoError(t, err)

	output := task9PushAll(t, stream, []model.SSEFrame{{
		Event: "error",
		Data:  []byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`),
	}})
	require.NoError(t, task9CloseStream(t, stream, &output))

	assert.Equal(t, []string{"response.failed"}, task9EventNames(output))
	assert.NotContains(t, task9EventNames(output), "response.completed")
	var event struct {
		Response responses.Response `json:"response"`
	}
	task9DecodeEvent(t, output[0], &event)
	require.NotNil(t, event.Response.Error)
	assert.Equal(t, "overloaded_error", event.Response.Error.Code)
	assert.Equal(t, "busy", event.Response.Error.Message)
}

func TestResponsesToAnthropicStreamRejectsInvalidCompletedArguments(t *testing.T) {
	stream, err := NewResponsesToAnthropicStream(task9ExchangeFor("claude-3", "gpt-5"))
	require.NoError(t, err)
	frames := []model.SSEFrame{
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`)},
		{Event: "response.function_call_arguments.delta", Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{"}`)},
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`)},
	}
	_, err = stream.Push(context.Background(), frames[0])
	require.NoError(t, err)
	_, err = stream.Push(context.Background(), frames[1])
	require.NoError(t, err)
	_, err = stream.Push(context.Background(), frames[2])

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestResponsesToAnthropicStreamRejectsInvalidUsage(t *testing.T) {
	stream, err := NewResponsesToAnthropicStream(task9ExchangeFor("claude-3", "gpt-5"))
	require.NoError(t, err)
	_, err = stream.Push(context.Background(), model.SSEFrame{
		Event: "response.completed",
		Data:  []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}}}}`),
	})

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_PROTOCOL, proxyErr.Kind)
}

func TestAnthropicResponsesStreamConstructorsRejectNilIDGenerator(t *testing.T) {
	exchange := model.Exchange{}

	_, err := NewAnthropicToResponsesStream(exchange)
	require.Error(t, err)
	_, err = NewResponsesToAnthropicStream(exchange)
	require.Error(t, err)
}

func TestAnthropicResponsesStreamCloseRejectsMissingTerminal(t *testing.T) {
	constructors := []struct {
		name string
		new  StreamTransformFactory
		part model.SSEFrame
	}{
		{name: "anthropic to responses", new: NewAnthropicToResponsesStream, part: model.SSEFrame{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude-3"}}`)}},
		{name: "responses to anthropic", new: NewResponsesToAnthropicStream, part: model.SSEFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`)}},
	}
	for _, tc := range constructors {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := tc.new(task9ExchangeFor("claude-3", "gpt-5"))
			require.NoError(t, err)
			_, err = stream.Push(context.Background(), tc.part)
			require.NoError(t, err)
			err = task9CloseStream(t, stream, nil)
			var proxyErr *model.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, model.ERROR_PROTOCOL, proxyErr.Kind)
		})
	}
}

func task9ExchangeFor(originalModel, translatedModel string) model.Exchange {
	next := 0
	return model.Exchange{
		OriginalRequest:   model.RequestEnvelope{Model: originalModel},
		TranslatedRequest: model.RequestEnvelope{Model: translatedModel},
		ProviderID:        "test-provider",
		NewID: func() string {
			next++
			return fmt.Sprintf("generated_%d", next)
		},
	}
}

func task9PushAll(t *testing.T, stream StreamTransform, input []model.SSEFrame) []model.SSEFrame {
	t.Helper()
	var output []model.SSEFrame
	for _, frame := range input {
		frames, err := stream.Push(context.Background(), frame)
		require.NoError(t, err)
		output = append(output, frames...)
	}
	return output
}

func task9CloseStream(t *testing.T, stream StreamTransform, output *[]model.SSEFrame) error {
	t.Helper()
	frames, err := stream.Close(context.Background())
	if output != nil {
		*output = append(*output, frames...)
	}
	return err
}

func task9EventNames(frames []model.SSEFrame) []string {
	names := make([]string, 0, len(frames))
	for _, frame := range frames {
		names = append(names, frame.Event)
	}
	return names
}

func task9DecodeEvent(t *testing.T, frame model.SSEFrame, target any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(frame.Data, target))
}

func task9CompletedResponse(t *testing.T, frames []model.SSEFrame) responses.Response {
	t.Helper()
	for _, frame := range frames {
		if frame.Event != "response.completed" {
			continue
		}
		var event struct {
			Response responses.Response `json:"response"`
		}
		task9DecodeEvent(t, frame, &event)
		return event.Response
	}
	t.Fatal("response.completed event not found")
	return responses.Response{}
}

func task9FullResponsesFrames() []model.SSEFrame {
	return []model.SSEFrame{
		{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`)},
		{Event: "response.reasoning_summary_text.delta", Data: []byte(`{"type":"response.reasoning_summary_text.delta","delta":"check"}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`)},
		{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","delta":"done"}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`)},
		{Event: "response.function_call_arguments.delta", Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":\"a.txt\"}"}`)},
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":4}}}`)},
	}
}

func task9FullAnthropicFrames() []model.SSEFrame {
	return []model.SSEFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"check"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"done"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":1}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"call_1","name":"read","input":{}}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.txt\"}"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":2}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":4}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
}
