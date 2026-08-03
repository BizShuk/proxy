package transform

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/responses"
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
			got, err := tc.fn(context.Background(), model.RequestEnvelope{
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

	result, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
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

func TestAnthropicToResponsesPreservesThinkingAsReasoningInput(t *testing.T) {
	body := []byte(`{
		"model":"grok-4.5-latest",
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"I should inspect the workspace.","signature":""},
				{"type":"tool_use","id":"call_1","name":"Bash","input":{"command":"pwd"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_1","content":"workspace path"}
			]}
		]
	}`)

	result, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "grok-4.5-latest", Stream: true, Body: body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(result.Body)
	require.NoError(t, err)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(request.Input, &input))
	require.Len(t, input, 3)
	assert.Equal(t, "reasoning", input[0]["type"])
	assert.Equal(t, []any{
		map[string]any{"type": "summary_text", "text": "I should inspect the workspace."},
	}, input[0]["summary"])
	assert.Equal(t, "function_call", input[1]["type"])
	assert.Equal(t, "function_call_output", input[2]["type"])
}

func TestAnthropicToResponsesPreservesEmptyReasoningSummary(t *testing.T) {
	signature, err := encodeResponsesReasoningSignature(responsesReasoningSignature{
		ID: "reasoning_1", EncryptedContent: "encrypted-reasoning",
	})
	require.NoError(t, err)

	body, err := anthropic.Encode(anthropic.Request{
		Model: "gpt-5.6-sol",
		Messages: []anthropic.Message{{
			Role: "assistant",
			Content: anthropic.ContentList{{
				Type: "thinking", Signature: signature,
			}},
		}},
	})
	require.NoError(t, err)

	result, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "gpt-5.6-sol", Stream: true, Body: body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(result.Body)
	require.NoError(t, err)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(request.Input, &input))
	require.Len(t, input, 1)
	assert.Equal(t, "reasoning", input[0]["type"])
	assert.Equal(t, "reasoning_1", input[0]["id"])
	assert.Equal(t, "encrypted-reasoning", input[0]["encrypted_content"])
	summary, exists := input[0]["summary"]
	require.True(t, exists, "Responses reasoning input requires summary even when it is empty")
	assert.Equal(t, []any{}, summary)
}

func TestResponsesReasoningRoundTripPreservesCompleteReplayItem(t *testing.T) {
	responseBody := []byte(`{
		"id":"resp_1",
		"object":"response",
		"model":"grok-4.5-latest",
		"status":"completed",
		"output":[
			{
				"id":"reasoning_1",
				"type":"reasoning",
				"summary":[
					{"type":"summary_text","text":"I should inspect."},
					{"type":"summary_text","text":"Then use the tool."}
				],
				"content":[{"type":"reasoning_text","text":"opaque reasoning text"}],
				"encrypted_content":"encrypted-reasoning",
				"status":"incomplete"
			},
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"Bash",
				"arguments":"{\"command\":\"pwd\"}"
			}
		]
	}`)

	anthropicResult, err := ResponsesToAnthropicResponse(context.Background(), model.ResponseEnvelope{
		Body: responseBody,
		Exchange: model.Exchange{
			OriginalRequest: model.RequestEnvelope{Model: "grok-4.5-latest"},
		},
	})
	require.NoError(t, err)
	anthropicResponse, err := anthropic.DecodeResponse(anthropicResult.Body)
	require.NoError(t, err)
	require.Len(t, anthropicResponse.Content, 2)
	require.Equal(t, "thinking", anthropicResponse.Content[0].Type)
	require.NotEmpty(t, anthropicResponse.Content[0].Signature)

	requestBody, err := anthropic.Encode(anthropic.Request{
		Model: "grok-4.5-latest",
		Messages: []anthropic.Message{
			{Role: "assistant", Content: anthropicResponse.Content},
			{Role: "user", Content: anthropic.ContentList{{
				Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(`"workspace path"`),
			}}},
		},
	})
	require.NoError(t, err)

	responsesResult, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "grok-4.5-latest", Stream: true, Body: requestBody,
	})
	require.NoError(t, err)
	responsesRequest, err := responses.DecodeRequest(responsesResult.Body)
	require.NoError(t, err)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(responsesRequest.Input, &input))
	require.Len(t, input, 3)
	assert.Equal(t, "reasoning_1", input[0]["id"])
	assert.Equal(t, "encrypted-reasoning", input[0]["encrypted_content"])
	assert.Equal(t, []any{
		map[string]any{"type": "summary_text", "text": "I should inspect."},
		map[string]any{"type": "summary_text", "text": "Then use the tool."},
	}, input[0]["summary"])
	assert.Equal(t, []any{
		map[string]any{"type": "reasoning_text", "text": "opaque reasoning text"},
	}, input[0]["content"])
	assert.Equal(t, "incomplete", input[0]["status"])
	assert.Equal(t, "function_call", input[1]["type"])
	assert.Equal(t, "function_call_output", input[2]["type"])
}

func TestResponsesToAnthropicRejectsPreviousResponseWithoutHistory(t *testing.T) {
	body := []byte(`{"model":"gpt","previous_response_id":"resp_1","input":[{"role":"user","content":"next"}]}`)
	_, err := ResponsesToAnthropicRequest(context.Background(), model.RequestEnvelope{Model: "claude", Body: body})
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
	assert.Equal(t, "stateful_context_not_portable", proxyErr.Code)
}

func TestResponsesToAnthropicRejectsProviderBuiltInTools(t *testing.T) {
	for _, toolType := range []string{"web_search", "x_search", "code_interpreter", "mcp"} {
		t.Run(toolType, func(t *testing.T) {
			body := []byte(`{"model":"gpt","input":"search","tools":[{"type":"` + toolType + `"}]}`)
			_, err := ResponsesToAnthropicRequest(context.Background(), model.RequestEnvelope{Model: "claude", Body: body})
			var proxyErr *model.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
			assert.Equal(t, "unsupported_tool", proxyErr.Code)
		})
	}
}

func TestAnthropicResponsesRequestTransformsRejectInvalidJSON(t *testing.T) {
	tests := []RequestTransform{AnthropicToResponsesRequest, ResponsesToAnthropicRequest}
	for _, transform := range tests {
		_, err := transform(context.Background(), model.RequestEnvelope{Model: "target", Body: []byte(`{`)})
		var proxyErr *model.ProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, model.ERROR_INVALID_REQUEST, proxyErr.Kind)
	}
}

func TestAnthropicResponsesRequestTransformsHonorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := AnthropicToResponsesRequest(ctx, model.RequestEnvelope{Model: "target", Body: []byte(task5AnthropicRequest)})
	require.ErrorIs(t, err, context.Canceled)
}

func task5MustFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}

func TestTask5ErrorsPreserveJSONSyntaxCause(t *testing.T) {
	_, err := ResponsesToAnthropicRequest(context.Background(), model.RequestEnvelope{Model: "target", Body: []byte(`{`)})
	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
}

func TestAnthropicToResponsesMapsMaxTokensToMaxOutputTokens(t *testing.T) {
	body := []byte(`{
		"model":"claude-source",
		"max_tokens":512,
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	result, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "gpt-5.5", Stream: true, Body: body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(result.Body)
	require.NoError(t, err)
	require.NotNil(t, request.MaxOutputTokens)
	assert.Equal(t, 512, *request.MaxOutputTokens)

	for _, loss := range result.Losses {
		assert.NotEqualf(t, "max_tokens", loss.Field,
			"max_tokens must not be reported as a loss after mapping is in place; got loss=%+v", loss)
	}
}

func TestAnthropicToResponsesOmitsMaxOutputTokensWhenAbsent(t *testing.T) {
	body := []byte(`{
		"model":"claude-source",
		"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]
	}`)

	result, err := AnthropicToResponsesRequest(context.Background(), model.RequestEnvelope{
		Model: "gpt-5.5", Stream: true, Body: body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(result.Body)
	require.NoError(t, err)
	assert.Nil(t, request.MaxOutputTokens)
}

func TestResponsesToAnthropicMapsMaxOutputTokensToMaxTokens(t *testing.T) {
	body := []byte(`{
		"model":"gpt-source",
		"max_output_tokens":256,
		"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]
	}`)

	result, err := ResponsesToAnthropicRequest(context.Background(), model.RequestEnvelope{
		Model: "claude", Body: body,
	})
	require.NoError(t, err)

	request, err := anthropic.DecodeRequest(result.Body)
	require.NoError(t, err)
	assert.Equal(t, 256, request.MaxTokens)
}
