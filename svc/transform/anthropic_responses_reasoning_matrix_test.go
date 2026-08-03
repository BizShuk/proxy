package transform

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/responses"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesReasoningReplayRoundTripMatrix(t *testing.T) {
	catalog, err := upstream.DefaultCatalog()
	require.NoError(t, err)

	summaries := []struct {
		name  string
		value responses.ContentList
	}{
		{name: "empty", value: responses.ContentList{}},
		{name: "non-empty", value: responses.ContentList{{Type: "summary_text", Text: "checked"}}},
	}
	streams := []struct {
		name  string
		value bool
	}{
		{name: "non-streaming", value: false},
		{name: "streaming", value: true},
	}
	profileIDs := []string{"openai-api", "openai-codex-oauth"}

	for _, summary := range summaries {
		for _, stream := range streams {
			for _, profileID := range profileIDs {
				name := summary.name + "/" + stream.name + "/" + profileID
				t.Run(name, func(t *testing.T) {
					profile, ok := catalog.Lookup(profileID)
					require.True(t, ok)

					upstreamStream := stream.value || profileID == "openai-codex-oauth"
					assistantContent := reasoningReplayAssistantContent(t, summary.value, upstreamStream)
					requestBody, err := anthropic.Encode(anthropic.Request{
						Model: "gpt-5.6-sol",
						Messages: []anthropic.Message{
							{Role: "assistant", Content: assistantContent},
							{Role: "user", Content: anthropic.ContentList{{
								Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(`"workspace path"`),
							}}},
						},
					})
					require.NoError(t, err)

					transformed, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
						Model: "gpt-5.6-sol", Stream: stream.value, Body: requestBody,
					})
					require.NoError(t, err)
					normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
						TargetFormat: model.FORMAT_OPENAI_RESPONSES,
						Model:        "gpt-5.6-sol",
						Stream:       stream.value,
						Body:         transformed.Body,
					})
					require.NoError(t, err)

					assert.Equal(t, upstreamStream, normalized.UpstreamStream)
					assert.Equal(t, profileID == "openai-codex-oauth" && !stream.value, normalized.BridgeToNonStream)
					assertReasoningReplayInput(t, normalized.Body, summary.value)
				})
			}
		}
	}
}

func reasoningReplayAssistantContent(t *testing.T, summary responses.ContentList, stream bool) anthropic.ContentList {
	t.Helper()
	if !stream {
		responseBody := reasoningReplayResponseBody(t, summary)
		result, err := ResponsesToAnthropicResponse(context.Background(), model.ResponseEnvelope{
			Body: responseBody,
			Exchange: model.Exchange{
				OriginalRequest: model.RequestEnvelope{Model: "gpt-5.6-sol"},
			},
		})
		require.NoError(t, err)
		response, err := anthropic.DecodeResponse(result.Body)
		require.NoError(t, err)
		return response.Content
	}

	exchange := task9ExchangeFor("gpt-5.6-sol", "gpt-5.6-sol")
	streamTransform, err := NewResponsesToAnthropicStream(exchange)
	require.NoError(t, err)
	collector, err := NewStreamCollector(model.FORMAT_ANTHROPIC_MESSAGES, exchange)
	require.NoError(t, err)
	for _, frame := range reasoningReplayResponseFrames(t, summary) {
		translated, err := streamTransform.Push(context.Background(), frame)
		require.NoError(t, err)
		for _, translatedFrame := range translated {
			require.NoError(t, collector.Push(context.Background(), translatedFrame))
		}
	}
	closing, err := streamTransform.Close(context.Background())
	require.NoError(t, err)
	for _, frame := range closing {
		require.NoError(t, collector.Push(context.Background(), frame))
	}
	result, err := collector.Close(context.Background())
	require.NoError(t, err)
	response, err := anthropic.DecodeResponse(result.Body)
	require.NoError(t, err)
	return response.Content
}

func reasoningReplayResponseBody(t *testing.T, summary responses.ContentList) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "resp_1", "object": "response", "model": "gpt-5.6-sol", "status": "completed",
		"output": reasoningReplayOutput(summary),
		"usage":  map[string]any{"input_tokens": 10, "output_tokens": 4, "total_tokens": 14},
	})
	require.NoError(t, err)
	return body
}

func reasoningReplayResponseFrames(t *testing.T, summary responses.ContentList) []model.SSEFrame {
	t.Helper()
	reasoning := reasoningReplayOutput(summary)[0]
	message := reasoningReplayOutput(summary)[1]
	functionCall := reasoningReplayOutput(summary)[2]
	frames := []model.SSEFrame{
		reasoningReplayFrame(t, "response.created", map[string]any{
			"type": "response.created", "response": map[string]any{
				"id": "resp_1", "model": "gpt-5.6-sol", "status": "in_progress",
			},
		}),
		reasoningReplayFrame(t, "response.output_item.added", map[string]any{
			"type": "response.output_item.added", "item": map[string]any{
				"id": "reasoning_1", "type": "reasoning", "summary": []any{}, "status": "in_progress",
			},
		}),
	}
	if len(summary) > 0 {
		frames = append(frames, reasoningReplayFrame(t, "response.reasoning_summary_text.delta", map[string]any{
			"type": "response.reasoning_summary_text.delta", "item_id": "reasoning_1", "delta": summary[0].Text,
		}))
	}
	frames = append(frames,
		reasoningReplayFrame(t, "response.output_item.done", map[string]any{
			"type": "response.output_item.done", "item": reasoning,
		}),
		reasoningReplayFrame(t, "response.output_item.added", map[string]any{
			"type": "response.output_item.added", "item": map[string]any{
				"id": "message_1", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{},
			},
		}),
		reasoningReplayFrame(t, "response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "item_id": "message_1", "delta": "running tool",
		}),
		reasoningReplayFrame(t, "response.output_item.done", map[string]any{
			"type": "response.output_item.done", "item": message,
		}),
		reasoningReplayFrame(t, "response.output_item.added", map[string]any{
			"type": "response.output_item.added", "item": map[string]any{
				"id": "function_1", "type": "function_call", "call_id": "call_1", "name": "Bash", "arguments": "",
			},
		}),
		reasoningReplayFrame(t, "response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": "function_1", "call_id": "call_1", "delta": `{"command":"pwd"}`,
		}),
		reasoningReplayFrame(t, "response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "function_1", "call_id": "call_1", "arguments": `{"command":"pwd"}`,
		}),
		reasoningReplayFrame(t, "response.output_item.done", map[string]any{
			"type": "response.output_item.done", "item": functionCall,
		}),
		reasoningReplayFrame(t, "response.completed", map[string]any{
			"type": "response.completed", "response": map[string]any{
				"id": "resp_1", "model": "gpt-5.6-sol", "status": "completed",
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 4, "total_tokens": 14},
			},
		}),
	)
	return frames
}

func reasoningReplayOutput(summary responses.ContentList) []any {
	return []any{
		map[string]any{
			"id": "reasoning_1", "type": "reasoning", "summary": summary,
			"content":           []any{map[string]any{"type": "reasoning_text", "text": "opaque reasoning text"}},
			"encrypted_content": "encrypted-reasoning", "status": "completed",
		},
		map[string]any{
			"id": "message_1", "type": "message", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "running tool"}},
		},
		map[string]any{
			"id": "function_1", "type": "function_call", "call_id": "call_1", "name": "Bash", "arguments": `{"command":"pwd"}`,
		},
	}
}

func reasoningReplayFrame(t *testing.T, event string, payload any) model.SSEFrame {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	return model.SSEFrame{Event: event, Data: body}
}

func assertReasoningReplayInput(t *testing.T, body []byte, summary responses.ContentList) {
	t.Helper()
	var request map[string]any
	require.NoError(t, json.Unmarshal(body, &request))
	input, ok := request["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 4)

	reasoning, ok := input[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "reasoning", reasoning["type"])
	assert.Equal(t, "reasoning_1", reasoning["id"])
	assert.Equal(t, "encrypted-reasoning", reasoning["encrypted_content"])
	assert.Equal(t, "completed", reasoning["status"])
	assert.Equal(t, []any{
		map[string]any{"type": "reasoning_text", "text": "opaque reasoning text"},
	}, reasoning["content"])
	require.Contains(t, reasoning, "summary")
	if len(summary) == 0 {
		assert.Equal(t, []any{}, reasoning["summary"])
	} else {
		assert.Equal(t, []any{map[string]any{"type": "summary_text", "text": "checked"}}, reasoning["summary"])
	}

	assert.Equal(t, "message", input[1].(map[string]any)["type"])
	assert.Equal(t, "function_call", input[2].(map[string]any)["type"])
	assert.Equal(t, "function_call_output", input[3].(map[string]any)["type"])
	for index := 1; index < len(input); index++ {
		assert.NotContains(t, input[index].(map[string]any), "summary")
	}
}
