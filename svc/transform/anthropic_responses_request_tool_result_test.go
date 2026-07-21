package transform

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTask5AnthropicToolOutput_LiftsImage ensures the transform no
// longer rejects tool_result blocks whose ContentList contains
// non-text parts (e.g. images returned by a Bash screenshot tool).
// The image bytes themselves are dropped — Responses API
// function_call_output only accepts a string — but a textual
// placeholder preserves the call shape so the conversation
// continues instead of returning 400.
func TestTask5AnthropicToolOutput_LiftsImage(t *testing.T) {
	imgBlock := anthropic.Content{
		Type: "image",
		Source: &anthropic.Source{
			Type:      "base64",
			MediaType: "image/png",
			Data:      "aW1ncGF5bG9hZA==",
		},
	}
	raw, err := json.Marshal(anthropic.ContentList{
		{Type: "text", Text: "screenshot captured"},
		imgBlock,
	})
	require.NoError(t, err)

	out, err := task5AnthropicToolOutput(anthropic.Content{
		Type:      "tool_result",
		ToolUseID: "toolu_test",
		Content:   raw,
	})
	require.NoError(t, err)
	assert.Contains(t, out, "screenshot captured")
	assert.Contains(t, out, "[image omitted: media_type=image/png,")
	assert.NotContains(t, out, "toolu_test") // tool_use_id lives on InputItem, not the output string
}

// TestTask5AnthropicToolOutput_PlainStringContent covers the
// common case of an Anthropic tool_result whose Content is a
// plain string (server returns text directly).
func TestTask5AnthropicToolOutput_PlainStringContent(t *testing.T) {
	raw, err := json.Marshal("hello world")
	require.NoError(t, err)
	out, err := task5AnthropicToolOutput(anthropic.Content{
		Type:      "tool_result",
		ToolUseID: "toolu_1",
		Content:   raw,
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", out)
}

// TestTask5AnthropicToolOutput_UnknownBlock ensures unknown block
// types render a placeholder rather than failing the whole
// tool_result. This preserves the conversation when a new
// Anthropic content type appears before the transform is taught it.
func TestTask5AnthropicToolOutput_UnknownBlock(t *testing.T) {
	raw, err := json.Marshal(anthropic.ContentList{
		{Type: "text", Text: "ok "},
		{Type: "audio", Text: "skipped"}, // anthropic has no audio block; synthetic for test
	})
	require.NoError(t, err)
	out, err := task5AnthropicToolOutput(anthropic.Content{
		Type:    "tool_result",
		Content: raw,
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(out, "ok "))
	assert.Contains(t, out, "[unsupported audio block omitted]")
}

// TestTask5AnthropicToolOutput_InvalidJSON surfaces the existing
// invalid_request error when tool_result.Content is not parseable.
func TestTask5AnthropicToolOutput_InvalidJSON(t *testing.T) {
	_, err := task5AnthropicToolOutput(anthropic.Content{
		Type:    "tool_result",
		Content: []byte("{not-json"),
	})
	require.Error(t, err)
}