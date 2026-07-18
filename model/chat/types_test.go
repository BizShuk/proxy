package chat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageContentAcceptsStringNullOrParts(t *testing.T) {
	tests := []struct {
		raw       string
		wantText  string
		wantParts int
		wantNil   bool
	}{
		{raw: `{"role":"user","content":"hello"}`, wantText: "hello"},
		{raw: `{"role":"assistant","content":null}`, wantNil: true},
		{raw: `{"role":"user","content":[{"type":"text","text":"hello"}]}`, wantParts: 1},
	}
	for _, tc := range tests {
		var message Message
		require.NoError(t, json.Unmarshal([]byte(tc.raw), &message))
		if tc.wantNil {
			assert.Nil(t, message.Content)
			continue
		}
		require.NotNil(t, message.Content)
		assert.Equal(t, tc.wantText, message.Content.Text)
		assert.Len(t, message.Content.Parts, tc.wantParts)
	}
}

func TestDecodeRequestRejectsInvalidContentScalar(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"model":"gpt","messages":[{"role":"user","content":42}]}`))
	require.Error(t, err)
}

func TestStreamToolCallPreservesIndexAndPartialArguments(t *testing.T) {
	var chunk StreamChunk
	err := json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"call_1","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`), &chunk)
	require.NoError(t, err)
	require.Len(t, chunk.Choices, 1)
	require.Len(t, chunk.Choices[0].Delta.ToolCalls, 1)
	assert.Equal(t, 2, chunk.Choices[0].Delta.ToolCalls[0].Index)
	assert.Equal(t, `{"path":`, chunk.Choices[0].Delta.ToolCalls[0].Function.Arguments)
}
