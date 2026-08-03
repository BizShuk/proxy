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

func TestDecodeResponseAcceptsNullableReasoningContent(t *testing.T) {
	response, err := DecodeResponse([]byte(`{
		"id":"resp_1",
		"output":[{
			"id":"reasoning_1",
			"type":"reasoning",
			"summary":[],
			"content":null,
			"encrypted_content":null,
			"status":null
		}]
	}`))
	require.NoError(t, err)
	require.Len(t, response.Output, 1)
	assert.Nil(t, response.Output[0].Content)
}

func TestDecodeInputRejectsInvalidReasoningSummaryVariants(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "missing", raw: `[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","encrypted_content":"encrypted"}]`},
		{name: "null", raw: `[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","summary":null,"encrypted_content":"encrypted"}]`},
		{name: "string", raw: `[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","summary":"not-an-array","encrypted_content":"encrypted"}]`},
		{name: "object", raw: `[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","summary":{},"encrypted_content":"encrypted"}]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeInput(json.RawMessage(tc.raw))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "input[1].summary")
		})
	}
}

func TestDecodeInputAcceptsReasoningSummaryArrays(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want ContentList
	}{
		{name: "empty", raw: `[{"type":"reasoning","summary":[],"encrypted_content":"encrypted"}]`, want: ContentList{}},
		{name: "non-empty", raw: `[{"type":"reasoning","summary":[{"type":"summary_text","text":"checked"}],"encrypted_content":"encrypted"}]`, want: ContentList{{Type: "summary_text", Text: "checked"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items, err := DecodeInput(json.RawMessage(tc.raw))
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.NotNil(t, items[0].Summary)
			assert.Equal(t, tc.want, *items[0].Summary)
		})
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
