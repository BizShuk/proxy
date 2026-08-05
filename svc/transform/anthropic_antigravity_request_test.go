package transform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/antigravity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func antigravityRequest(t *testing.T, body string, stream bool) antigravity.Request {
	t.Helper()
	result, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		SourceFormat: model.FORMAT_ANTHROPIC_MESSAGES,
		TargetFormat: model.FORMAT_ANTIGRAVITY,
		Model:        "gemini-3.1-pro-high",
		Stream:       stream,
		Body:         []byte(body),
	})
	require.NoError(t, err)

	var decoded antigravity.Request
	require.NoError(t, json.Unmarshal(result.Body, &decoded))
	return decoded
}

// The gateway routes on the envelope, not on the Gemini payload, so every
// envelope field it keys off must be present and correctly named.
func TestAnthropicToAntigravityRequestBuildsEnvelope(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "gemini-3.1-pro-high",
		"max_tokens": 512,
		"system": [{"type": "text", "text": "be terse"}],
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Inspect a.txt"}]}]
	}`, false)

	assert.Equal(t, "gemini-3.1-pro-high", decoded.Model)
	assert.Equal(t, antigravity.USER_AGENT, decoded.UserAgent)
	assert.Equal(t, antigravity.REQUEST_TYPE_AGENT, decoded.RequestType)
	assert.True(t, strings.HasPrefix(decoded.RequestID, ANTIGRAVITY_REQUEST_ID_PREFIX))
	// project is credential-derived and must not be guessed by the transform.
	assert.Empty(t, decoded.Project)

	require.NotNil(t, decoded.Request.SystemInstruction)
	assert.Equal(t, antigravity.ROLE_USER, decoded.Request.SystemInstruction.Role)
	require.Len(t, decoded.Request.SystemInstruction.Parts, 1)
	assert.Equal(t, "be terse", decoded.Request.SystemInstruction.Parts[0].Text)

	require.Len(t, decoded.Request.Contents, 1)
	assert.Equal(t, antigravity.ROLE_USER, decoded.Request.Contents[0].Role)
	require.Len(t, decoded.Request.Contents[0].Parts, 1)
	assert.Equal(t, "Inspect a.txt", decoded.Request.Contents[0].Parts[0].Text)

	require.NotNil(t, decoded.Request.GenerationConfig)
	assert.Equal(t, 512, decoded.Request.GenerationConfig.MaxOutputTokens)
	assert.NotEmpty(t, decoded.Request.SessionID)
}

// The same conversation must reuse one session handle across retries, or the
// gateway treats every attempt as a new session.
func TestAnthropicToAntigravityRequestSessionIDIsStable(t *testing.T) {
	body := `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
	first := antigravityRequest(t, body, false)
	second := antigravityRequest(t, body, false)

	assert.Equal(t, first.Request.SessionID, second.Request.SessionID)
	assert.NotEqual(t, first.RequestID, second.RequestID)
}

// The gateway validates tool schemas as protobuf Schema under the key
// `parameters`; emitting `parametersJsonSchema` gets the tool silently dropped.
func TestAnthropicToAntigravityRequestUsesParametersKey(t *testing.T) {
	result, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		Model: "claude-sonnet-4-6",
		Body: []byte(`{
			"model": "claude-sonnet-4-6",
			"max_tokens": 64,
			"messages": [{"role": "user", "content": [{"type": "text", "text": "go"}]}],
			"tools": [{"name": "read_file", "description": "read", "input_schema": {"type": "object"}}]
		}`),
	})
	require.NoError(t, err)

	assert.Contains(t, string(result.Body), `"parameters"`)
	assert.NotContains(t, string(result.Body), "parametersJsonSchema")

	var decoded antigravity.Request
	require.NoError(t, json.Unmarshal(result.Body, &decoded))
	require.Len(t, decoded.Request.Tools, 1)
	require.Len(t, decoded.Request.Tools[0].FunctionDeclarations, 1)
	assert.Equal(t, "read_file", decoded.Request.Tools[0].FunctionDeclarations[0].Name)
	// agentsdk owns the dialect conversion: types are uppercased, and a
	// zero-argument tool gains a placeholder property because Google rejects a
	// property-less OBJECT.
	assert.JSONEq(t, `{
		"type": "OBJECT",
		"properties": {"_placeholder": {
			"type": "BOOLEAN",
			"description": "Technical placeholder to ensure a non-empty schema"
		}},
		"required": ["_placeholder"]
	}`, string(decoded.Request.Tools[0].FunctionDeclarations[0].Parameters))
}

// tool_result carries only the tool_use id, but functionResponse is keyed by
// name, so the mapping has to survive across messages.
func TestAnthropicToAntigravityRequestResolvesToolResultName(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "m", "max_tokens": 64,
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "read it"}]},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_1", "name": "read_file", "input": {"path": "a.txt"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "file body"}
			]}
		]
	}`, false)

	require.Len(t, decoded.Request.Contents, 3)
	call := decoded.Request.Contents[1].Parts[0].FunctionCall
	require.NotNil(t, call)
	assert.Equal(t, "read_file", call.Name)

	response := decoded.Request.Contents[2].Parts[0].FunctionResponse
	require.NotNil(t, response)
	assert.Equal(t, "read_file", response.Name)
	assert.Equal(t, "toolu_1", response.ID)
	assert.Equal(t, map[string]any{"result": "file body"}, response.Response)
}

// A tool result carrying an image becomes two parts: the functionResponse and
// a sibling inlineData part. agentsdk's FunctionResponse has no parts field to
// nest media under, and the gateway reads it from either position.
func TestAnthropicToAntigravityRequestLiftsToolResultImages(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "m", "max_tokens": 64,
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_1", "name": "screenshot", "input": {}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": [
					{"type": "text", "text": "captured"},
					{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "AAAA"}}
				]}
			]}
		]
	}`, false)

	parts := decoded.Request.Contents[1].Parts
	require.Len(t, parts, 2)

	response := parts[0].FunctionResponse
	require.NotNil(t, response)
	// The non-image block is carried through whole, not flattened to its text.
	assert.Equal(t, map[string]any{
		"result": map[string]any{"type": "text", "text": "captured"},
	}, response.Response)

	require.NotNil(t, parts[1].InlineData)
	assert.Equal(t, "image/png", parts[1].InlineData.MIMEType)
	assert.Equal(t, "AAAA", parts[1].InlineData.Data)
}

// The gateway splits a model turn at each functionCall, so a text part after a
// call would insert an extra assistant turn between tool_use and tool_result.
func TestAnthropicToAntigravityRequestOrdersModelParts(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "m", "max_tokens": 64,
		"messages": [{"role": "assistant", "content": [
			{"type": "tool_use", "id": "toolu_1", "name": "run", "input": {}},
			{"type": "text", "text": "after"},
			{"type": "thinking", "thinking": "ponder", "signature": "sig-1"}
		]}]
	}`, false)

	parts := decoded.Request.Contents[0].Parts
	require.Len(t, parts, 3)
	assert.True(t, parts[0].Thought)
	assert.Equal(t, "after", parts[1].Text)
	assert.NotNil(t, parts[2].FunctionCall)
}

// An unsigned thinking block cannot be replayed: the gateway validates thought
// signatures, so forwarding it fails the whole request.
func TestAnthropicToAntigravityRequestDropsUnsignedThinking(t *testing.T) {
	result, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		Model: "m",
		Body: []byte(`{
			"model": "m", "max_tokens": 64,
			"messages": [{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "unsigned"},
				{"type": "text", "text": "answer"}
			]}]
		}`),
	})
	require.NoError(t, err)
	assert.NotContains(t, string(result.Body), "unsigned")
	require.NotEmpty(t, result.Losses)
	assert.Contains(t, result.Losses[0].Reason, "signature")
}

func TestAnthropicToAntigravityRequestMapsToolChoiceAndThinking(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "m", "max_tokens": 64,
		"messages": [{"role": "user", "content": [{"type": "text", "text": "go"}]}],
		"tools": [{"name": "run", "input_schema": {"type": "object"}}],
		"tool_choice": {"type": "tool", "name": "run"},
		"thinking": {"type": "enabled", "budget_tokens": 4096}
	}`, false)

	require.NotNil(t, decoded.Request.ToolConfig)
	assert.Equal(t, antigravity.MODE_ANY, decoded.Request.ToolConfig.FunctionCallingConfig.Mode)

	// Gemini spells the thinking control in camelCase; Claude uses snake_case.
	thinking, ok := decoded.Request.GenerationConfig.ThinkingConfig.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(4096), thinking["thinkingBudget"])
	assert.Equal(t, true, thinking["includeThoughts"])
}

// A turn with no usable part must be dropped: the gateway rejects the whole
// request with "required oneof field 'data' must have one initialized field".
func TestAnthropicToAntigravityRequestSkipsEmptyTurns(t *testing.T) {
	decoded := antigravityRequest(t, `{
		"model": "m", "max_tokens": 64,
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": ""}]},
			{"role": "user", "content": [{"type": "text", "text": "real"}]}
		]
	}`, false)

	require.Len(t, decoded.Request.Contents, 1)
	assert.Equal(t, "real", decoded.Request.Contents[0].Parts[0].Text)
}

func TestAnthropicToAntigravityRequestRejectsUnknownBlock(t *testing.T) {
	_, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		Model: "m",
		Body: []byte(`{
			"model": "m", "max_tokens": 64,
			"messages": [{"role": "user", "content": [{"type": "document", "text": "x"}]}]
		}`),
	})
	require.ErrorContains(t, err, "document")
}
