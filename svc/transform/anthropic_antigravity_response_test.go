package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func antigravityResponse(t *testing.T, body string) *anthropic.Response {
	t.Helper()
	result, err := AntigravityToAnthropicResponse(context.Background(), model.ResponseEnvelope{
		Status: 200,
		Body:   []byte(body),
		Exchange: model.Exchange{
			OriginalRequest: model.RequestEnvelope{Model: "gemini-3.1-pro-high"},
			NewID:           func() string { return "msg_generated" },
		},
	})
	require.NoError(t, err)

	decoded, err := anthropic.DecodeResponse(result.Body)
	require.NoError(t, err)
	return decoded
}

func TestAntigravityToAnthropicResponseMapsTextAndUsage(t *testing.T) {
	decoded := antigravityResponse(t, `{"response":{
		"responseId": "resp_1",
		"modelVersion": "gemini-3.1-pro-high",
		"candidates": [{"content": {"role": "model", "parts": [{"text": "done"}]}, "finishReason": "STOP"}],
		"usageMetadata": {"promptTokenCount": 5, "candidatesTokenCount": 1, "totalTokenCount": 6}
	}}`)

	assert.Equal(t, "resp_1", decoded.ID)
	assert.Equal(t, "message", decoded.Type)
	assert.Equal(t, "assistant", decoded.Role)
	assert.Equal(t, "gemini-3.1-pro-high", decoded.Model)
	assert.Equal(t, "end_turn", decoded.StopReason)
	require.Len(t, decoded.Content, 1)
	assert.Equal(t, "done", decoded.Content[0].Text)
	assert.Equal(t, 5, decoded.Usage.InputTokens)
	assert.Equal(t, 1, decoded.Usage.OutputTokens)
}

// The gateway reports STOP even when the turn ends in a functionCall, so the
// finish reason alone would hand Anthropic clients the wrong stop_reason.
func TestAntigravityToAnthropicResponseMapsToolUseStopReason(t *testing.T) {
	decoded := antigravityResponse(t, `{"response":{
		"candidates": [{"content": {"role": "model", "parts": [
			{"functionCall": {"id": "call_1", "name": "read_file", "args": {"path": "a.txt"}}}
		]}, "finishReason": "STOP"}],
		"usageMetadata": {"promptTokenCount": 3, "candidatesTokenCount": 2}
	}}`)

	assert.Equal(t, "tool_use", decoded.StopReason)
	require.Len(t, decoded.Content, 1)
	assert.Equal(t, "tool_use", decoded.Content[0].Type)
	assert.Equal(t, "call_1", decoded.Content[0].ID)
	assert.Equal(t, "read_file", decoded.Content[0].Name)
	assert.JSONEq(t, `{"path":"a.txt"}`, string(decoded.Content[0].Input))
}

// Gemini counts cached tokens inside promptTokenCount; Anthropic reports them
// as a separate bucket, so double-counting would inflate the input total.
func TestAntigravityToAnthropicResponseSplitsCachedTokens(t *testing.T) {
	decoded := antigravityResponse(t, `{"response":{
		"candidates": [{"content": {"parts": [{"text": "hi"}]}, "finishReason": "STOP"}],
		"usageMetadata": {
			"promptTokenCount": 100, "cachedContentTokenCount": 40,
			"candidatesTokenCount": 7, "thoughtsTokenCount": 3
		}
	}}`)

	assert.Equal(t, 60, decoded.Usage.InputTokens)
	assert.Equal(t, 40, decoded.Usage.CacheReadInputTokens)
	assert.Equal(t, 10, decoded.Usage.OutputTokens)
}

func TestAntigravityToAnthropicResponseMapsThinking(t *testing.T) {
	decoded := antigravityResponse(t, `{"response":{
		"candidates": [{"content": {"parts": [
			{"thought": true, "text": "reason", "thoughtSignature": "sig-1"},
			{"text": "answer"}
		]}, "finishReason": "MAX_TOKENS"}],
		"usageMetadata": {"promptTokenCount": 1, "candidatesTokenCount": 1}
	}}`)

	assert.Equal(t, "max_tokens", decoded.StopReason)
	require.Len(t, decoded.Content, 2)
	assert.Equal(t, "thinking", decoded.Content[0].Type)
	assert.Equal(t, "reason", decoded.Content[0].Thinking)
	assert.Equal(t, "sig-1", decoded.Content[0].Signature)
	assert.Equal(t, "text", decoded.Content[1].Type)
}

// Some gateway hops return a bare Gemini payload with no "response" wrapper.
func TestAntigravityToAnthropicResponseAcceptsUnwrappedBody(t *testing.T) {
	decoded := antigravityResponse(t, `{
		"candidates": [{"content": {"parts": [{"text": "bare"}]}, "finishReason": "STOP"}],
		"usageMetadata": {"promptTokenCount": 2, "candidatesTokenCount": 1}
	}`)

	require.Len(t, decoded.Content, 1)
	assert.Equal(t, "bare", decoded.Content[0].Text)
}

func TestAntigravityToAnthropicResponseRejectsEmptyCandidates(t *testing.T) {
	_, err := AntigravityToAnthropicResponse(context.Background(), model.ResponseEnvelope{
		Status: 200,
		Body:   []byte(`{"response":{"usageMetadata":{"promptTokenCount":1}}}`),
	})
	require.Error(t, err)
	// protocolFailure keeps the diagnostic on the cause; the wire message is
	// deliberately generic so upstream detail never reaches the caller.
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	require.ErrorContains(t, proxyErr.Cause, "no candidates")
}
