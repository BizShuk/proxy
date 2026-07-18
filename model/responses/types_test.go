package responses

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestPreservesStatefulAndToolFields(t *testing.T) {
	raw := `{"model":"gpt-5","input":[{"role":"user","content":"hi"}],"previous_response_id":"resp_1","tools":[{"type":"web_search"}],"stream":true}`
	request, err := DecodeRequest([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, "resp_1", request.PreviousResponseID)
	require.Len(t, request.Tools, 1)
	assert.Equal(t, "web_search", request.Tools[0].Type)
	require.NotNil(t, request.Stream)
	assert.True(t, *request.Stream)

	items, err := DecodeInput(request.Input)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "user", items[0].Role)
	require.Len(t, items[0].Content, 1)
	assert.Equal(t, "hi", items[0].Content[0].Text)
}

func TestDecodeInputAcceptsStringOrArray(t *testing.T) {
	for _, raw := range []string{
		`"hello"`,
		`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]`,
	} {
		items, err := DecodeInput(json.RawMessage(raw))
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Len(t, items[0].Content, 1)
		assert.Equal(t, "hello", items[0].Content[0].Text)
	}
}

func TestDecodeRequestRejectsInvalidInputScalar(t *testing.T) {
	_, err := DecodeRequest([]byte(`{"model":"gpt","input":42}`))
	require.Error(t, err)
}

func TestToolParametersPreserveJSONNumber(t *testing.T) {
	request, err := DecodeRequest([]byte(`{"model":"gpt","input":"hi","tools":[{"type":"function","name":"read","parameters":{"const":9007199254740993}}]}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"const":9007199254740993}`, string(request.Tools[0].Parameters))
}
