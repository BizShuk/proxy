package transform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatResponsesRequestTransforms(t *testing.T) {
	tests := []struct {
		name       string
		fn         RequestTransform
		body       string
		want       string
		lossFields []string
	}{
		{
			name: "chat to responses",
			fn:   ChatToResponsesRequest,
			body: task6ChatRequestBody(),
			want: "responses_from_chat.json",
			lossFields: []string{
				"messages.role",
				"messages.reasoning_content",
				"max_completion_tokens",
				"temperature",
				"top_p",
				"stop",
			},
		},
		{
			name: "responses to chat",
			fn:   ResponsesToChatRequest,
			body: task6ResponsesRequestBody(),
			want: "chat_from_responses.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(context.Background(), model.RequestEnvelope{
				Model:  "target-model",
				Stream: true,
				Body:   []byte(tc.body),
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(task6Fixture(t, tc.want)), string(got.Body))
			var lossFields []string
			for _, loss := range got.Losses {
				lossFields = append(lossFields, loss.Field)
			}
			assert.ElementsMatch(t, tc.lossFields, lossFields)
		})
	}
}

func TestChatResponsesPreserveParallelToolCalls(t *testing.T) {
	chatResult, err := ChatToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "target",
		Body: []byte(`{
			"model":"source",
			"messages":[{"role":"user","content":"hi"}],
			"parallel_tool_calls":true
		}`),
	})
	require.NoError(t, err)
	assert.Contains(t, string(chatResult.Body), `"parallel_tool_calls":true`)

	responsesResult, err := ResponsesToChatRequest(context.Background(), model.RequestEnvelope{
		Model: "target",
		Body: []byte(`{
			"model":"source",
			"input":"hi",
			"parallel_tool_calls":true
		}`),
	})
	require.NoError(t, err)
	assert.Contains(t, string(responsesResult.Body), `"parallel_tool_calls":true`)
}

func TestChatToResponsesReportsDeveloperPriorityLoss(t *testing.T) {
	result, err := ChatToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "target-model",
		Body: []byte(`{
			"model":"source-chat",
			"messages":[
				{"role":"system","content":"system"},
				{"role":"developer","content":"developer"},
				{"role":"user","content":"hi"}
			]
		}`),
	})

	require.NoError(t, err)
	require.Len(t, result.Losses, 1)
	assert.Equal(t, "messages.role", result.Losses[0].Field)
	assert.JSONEq(t, `{
		"model":"target-model",
		"instructions":"system\ndeveloper",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"stream":false
	}`, string(result.Body))
}

func TestResponsesToChatRejectsNonportableState(t *testing.T) {
	_, err := ResponsesToChatRequest(context.Background(), model.RequestEnvelope{
		Model: "target-model",
		Body:  []byte(`{"model":"source","previous_response_id":"resp_1","input":"next"}`),
	})

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "stateful_context_not_portable", proxyErr.Code)
}

func TestResponsesToChatRejectsBuiltInTool(t *testing.T) {
	_, err := ResponsesToChatRequest(context.Background(), model.RequestEnvelope{
		Model: "target-model",
		Body:  []byte(`{"model":"source","input":"find news","tools":[{"type":"web_search"}]}`),
	})

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "unsupported_tool", proxyErr.Code)
}

func TestChatResponsesRequestTransformsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, fn := range []RequestTransform{ChatToResponsesRequest, ResponsesToChatRequest} {
		_, err := fn(ctx, model.RequestEnvelope{Body: []byte(`{}`)})
		require.ErrorIs(t, err, context.Canceled)
	}
}

func TestChatToResponsesRejectsUnsupportedMessageRole(t *testing.T) {
	_, err := ChatToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "target-model",
		Body:  []byte(`{"model":"source","messages":[{"role":"function","content":"legacy"}]}`),
	})

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "unsupported_message_role", proxyErr.Code)
}

func TestResponsesToChatRejectsUnsupportedInputContent(t *testing.T) {
	_, err := ResponsesToChatRequest(context.Background(), model.RequestEnvelope{
		Model: "target-model",
		Body: []byte(`{
			"model":"source",
			"input":[{"type":"message","role":"user","content":[{"type":"input_file","file_id":"file_1"}]}]
		}`),
	})

	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "unsupported_content", proxyErr.Code)
}

func TestChatResponsesInvalidJSONUsesTypedError(t *testing.T) {
	for _, fn := range []RequestTransform{ChatToResponsesRequest, ResponsesToChatRequest} {
		_, err := fn(context.Background(), model.RequestEnvelope{Model: "target", Body: []byte(`{`)})
		var proxyErr *model.ProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, model.ERROR_INVALID_REQUEST, proxyErr.Kind)
		assert.Equal(t, "invalid_request", proxyErr.Code)
		assert.True(t, errors.Is(proxyErr, proxyErr.Cause))
	}
}

func task6Fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}

func task6ChatRequestBody() string {
	return `{
		"model":"source-chat",
		"messages":[
			{"role":"system","content":"You are concise."},
			{"role":"developer","content":"Use approved tools."},
			{"role":"user","content":[
				{"type":"text","text":"inspect"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1n"}}
			]},
			{"role":"assistant","content":"checking","reasoning_content":"I should read the file","tool_calls":[
				{"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.txt\"}"}}
			]},
			{"role":"tool","tool_call_id":"call_1","name":"read","content":"ok"}
		],
		"max_completion_tokens":512,
		"temperature":0.2,
		"top_p":0.9,
		"stop":["STOP"],
		"tools":[{"type":"function","function":{"name":"read","description":"Read a file","parameters":{"type":"object"}}}],
		"tool_choice":{"type":"function","function":{"name":"read"}},
		"reasoning_effort":"medium",
		"stream":false
	}`
}

func task6ResponsesRequestBody() string {
	return `{
		"model":"source-responses",
		"instructions":"You are concise.",
		"input":[
			{"type":"message","role":"user","content":[
				{"type":"input_text","text":"inspect"},
				{"type":"input_image","image_url":"data:image/png;base64,aW1n"}
			]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking"}]},
			{"type":"function_call","call_id":"call_1","name":"read","arguments":"{\"path\":\"a.txt\"}"},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		],
		"tools":[{"type":"function","name":"read","description":"Read a file","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"read"},
		"reasoning":{"effort":"medium"},
		"stream":false
	}`
}
