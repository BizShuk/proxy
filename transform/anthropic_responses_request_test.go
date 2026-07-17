package transform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const task5AnthropicRequest = `{
  "model": "claude-source",
  "system": [{"type": "text", "text": "You are concise."}],
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "inspect"},
      {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aW1n"}}
    ]},
    {"role": "assistant", "content": [
      {"type": "text", "text": "checking"},
      {"type": "tool_use", "id": "call_1", "name": "read", "input": {"path": "a.txt"}}
    ]},
    {"role": "user", "content": [
      {"type": "tool_result", "tool_use_id": "call_1", "content": [{"type": "text", "text": "ok"}]}
    ]}
  ],
  "tools": [{"name": "read", "description": "Read a file", "input_schema": {"type": "object"}}],
  "tool_choice": {"type": "tool", "name": "read"},
  "temperature": 0.2,
  "top_p": 0.9,
  "thinking": {"type": "enabled", "budget_tokens": 2048}
}`

func TestAnthropicResponsesRequestTransforms(t *testing.T) {
	tests := []struct {
		name     string
		fn       RequestTransform
		body     []byte
		wantFile string
	}{
		{"anthropic to responses", AnthropicToResponsesRequest, []byte(task5AnthropicRequest), "responses_from_anthropic.json"},
		{"responses to anthropic", ResponsesToAnthropicRequest, task5MustFixture(t, "responses_request_full.json"), "anthropic_from_responses.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(context.Background(), protocol.RequestEnvelope{
				Model: "target-model", Stream: true, Body: tc.body,
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(task5MustFixture(t, tc.wantFile)), string(got.Body))
			assert.NotEmpty(t, got.Losses)
		})
	}
}

func TestAnthropicToResponsesPreservesInstructionMessageRoles(t *testing.T) {
	body := []byte(`{
		"model":"openai/gpt-5.5",
		"messages":[
			{"role":"system","content":"system policy"},
			{"role":"developer","content":[{"type":"text","text":"developer policy"}]},
			{"role":"user","content":"hello"}
		]
	}`)

	result, err := AnthropicToResponsesRequest(context.Background(), protocol.RequestEnvelope{
		Model: "gpt-5.5", Stream: true, Body: body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(result.Body)
	require.NoError(t, err)
	items, err := responses.DecodeInput(request.Input)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, "system", items[0].Role)
	assert.Equal(t, responses.ContentList{{Type: "input_text", Text: "system policy"}}, items[0].Content)
	assert.Equal(t, "developer", items[1].Role)
	assert.Equal(t, responses.ContentList{{Type: "input_text", Text: "developer policy"}}, items[1].Content)
	assert.Equal(t, "user", items[2].Role)
}

func TestResponsesToAnthropicRejectsPreviousResponseWithoutHistory(t *testing.T) {
	body := []byte(`{"model":"gpt","previous_response_id":"resp_1","input":[{"role":"user","content":"next"}]}`)
	_, err := ResponsesToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "stateful_context_not_portable", proxyErr.Code)
}

func TestResponsesToAnthropicRejectsProviderBuiltInTools(t *testing.T) {
	for _, toolType := range []string{"web_search", "x_search", "code_interpreter", "mcp"} {
		t.Run(toolType, func(t *testing.T) {
			body := []byte(`{"model":"gpt","input":"search","tools":[{"type":"` + toolType + `"}]}`)
			_, err := ResponsesToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
			assert.Equal(t, "unsupported_tool", proxyErr.Code)
		})
	}
}

func TestAnthropicResponsesRequestTransformsRejectInvalidJSON(t *testing.T) {
	tests := []RequestTransform{AnthropicToResponsesRequest, ResponsesToAnthropicRequest}
	for _, transform := range tests {
		_, err := transform(context.Background(), protocol.RequestEnvelope{Model: "target", Body: []byte(`{`)})
		var proxyErr *protocol.ProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, protocol.ERROR_INVALID_REQUEST, proxyErr.Kind)
	}
}

func TestAnthropicResponsesRequestTransformsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := AnthropicToResponsesRequest(ctx, protocol.RequestEnvelope{Model: "target", Body: []byte(task5AnthropicRequest)})
	require.ErrorIs(t, err, context.Canceled)
}

func task5MustFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}

func TestTask5ErrorsPreserveJSONSyntaxCause(t *testing.T) {
	_, err := ResponsesToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "target", Body: []byte(`{`)})
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
}
