package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageAcceptsStringOrBlocks(t *testing.T) {
	for _, raw := range []string{
		`{"role":"user","content":"hello"}`,
		`{"role":"user","content":[{"type":"text","text":"hello"}]}`,
	} {
		var message Message
		require.NoError(t, json.Unmarshal([]byte(raw), &message))
		require.Len(t, message.Content, 1)
		assert.Equal(t, "text", message.Content[0].Type)
		assert.Equal(t, "hello", message.Content[0].Text)
	}
}

func TestSystemAcceptsStringOrBlocks(t *testing.T) {
	request, err := DecodeRequest([]byte(`{"model":"claude","system":"be concise","messages":[{"role":"user","content":"hi"}]}`))
	require.NoError(t, err)
	require.Len(t, request.System, 1)
	assert.Equal(t, "be concise", request.System[0].Text)
}

func TestDecodeRequestRejectsBlankModelAndNegativeMaxTokens(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"model":" ","messages":[]}`))
	require.Error(t, err)
	_, err = DecodeRequest([]byte(`{"model":"claude","max_tokens":-1,"messages":[]}`))
	require.Error(t, err)
}

func TestToolInputPreservesJSONNumber(t *testing.T) {
	request, err := DecodeRequest([]byte(`{"model":"claude","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"read","input":{"line":9007199254740993}}]}]}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"line":9007199254740993}`, string(request.Messages[0].Content[0].Input))
}
