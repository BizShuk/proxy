package antigravity_test

import (
	"encoding/json"
	"testing"

	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	"github.com/bizshuk/proxy/model/antigravity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sanitized(t *testing.T, raw string) map[string]any {
	t.Helper()
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(antigravity.SanitizeSchema(json.RawMessage(raw)), &decoded))
	return decoded
}

// agentsdk substitutes a placeholder for `properties: {}` but not for a missing
// key, and Google rejects a property-less OBJECT either way. A zero-argument
// tool is commonly written as bare {"type":"object"}.
func TestSanitizeSchemaFillsMissingPropertiesKey(t *testing.T) {
	decoded := sanitized(t, `{"type":"object"}`)

	assert.Equal(t, "OBJECT", decoded["type"])
	properties, ok := decoded["properties"].(map[string]any)
	require.True(t, ok, "a property-less object schema must gain a placeholder")
	assert.NotEmpty(t, properties)
}

// A schema that already declares properties must pass through untouched.
func TestSanitizeSchemaKeepsDeclaredProperties(t *testing.T) {
	decoded := sanitized(t, `{"type":"object","properties":{"path":{"type":"string"}}}`)

	properties := decoded["properties"].(map[string]any)
	require.Contains(t, properties, "path")
	assert.NotContains(t, properties, "_placeholder")
}

// The dialect conversion itself belongs to agentsdk; this only pins that the
// proxy actually routes through it rather than emitting raw JSON Schema.
func TestSanitizeSchemaDelegatesDialectConversion(t *testing.T) {
	decoded := sanitized(t, `{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"additionalProperties": false,
		"properties": {"limit": {"type": "number"}}
	}`)

	assert.Equal(t, "OBJECT", decoded["type"])
	assert.NotContains(t, decoded, "$schema")
	assert.NotContains(t, decoded, "additionalProperties")
	assert.Equal(t, "NUMBER", decoded["properties"].(map[string]any)["limit"].(map[string]any)["type"])
}

// Non-object and unusable input must still yield a schema the gateway accepts.
func TestSanitizeSchemaFallsBackOnUnusableInput(t *testing.T) {
	for _, raw := range []string{"", "   ", "not json"} {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(antigravity.SanitizeSchema(json.RawMessage(raw)), &decoded), raw)
		assert.Equal(t, "OBJECT", decoded["type"], raw)
	}
}

// The identity and cached-token fields sit outside agentsdk's response type,
// but the proxy echoes them back as the Anthropic message id and model.
func TestDecodeResponseReadsFieldsOutsideAgentsdkType(t *testing.T) {
	body, err := antigravity.DecodeResponse([]byte(`{"response":{
		"responseId": "resp_1",
		"modelVersion": "gemini-3.1-pro-high",
		"candidates": [{"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}],
		"usageMetadata": {"promptTokenCount": 100, "cachedContentTokenCount": 40, "candidatesTokenCount": 7}
	}}`))
	require.NoError(t, err)

	assert.Equal(t, "resp_1", body.ResponseID)
	assert.Equal(t, "gemini-3.1-pro-high", body.ModelVersion)
	assert.Equal(t, 40, body.CachedTokenCount)
	require.Len(t, body.Candidates, 1)
	assert.Equal(t, "done", body.Candidates[0].Content.Parts[0].Text)
	require.NotNil(t, body.UsageMetadata)
	assert.Equal(t, 100, body.UsageMetadata.PromptTokenCount)
}

// Some gateway hops return the payload with no "response" wrapper.
func TestDecodeResponseAcceptsBareBody(t *testing.T) {
	body, err := antigravity.DecodeResponse([]byte(`{
		"responseId": "resp_bare",
		"candidates": [{"content":{"parts":[{"text":"bare"}]},"finishReason":"STOP"}],
		"usageMetadata": {"promptTokenCount": 3, "cachedContentTokenCount": 1}
	}`))
	require.NoError(t, err)

	assert.Equal(t, "resp_bare", body.ResponseID)
	assert.Equal(t, 1, body.CachedTokenCount)
	require.Len(t, body.Candidates, 1)
	assert.Equal(t, "bare", body.Candidates[0].Content.Parts[0].Text)
}

// The widened container must still serialize agentsdk's embedded fields, or the
// sampling additions would come at the cost of the fields it owns.
func TestGenerationConfigCarriesBothOwners(t *testing.T) {
	temperature, topP := 0.4, 0.9
	config := antigravity.GenerationConfig{Temperature: &temperature, TopP: &topP}
	config.MaxOutputTokens = 512
	config.StopSequences = []string{"STOP"}
	config.ThinkingConfig = ag.GeminiThinkingConfig{IncludeThoughts: true, ThinkingBudget: 4096}

	encoded, err := json.Marshal(config)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	assert.Equal(t, float64(512), decoded["maxOutputTokens"])
	assert.Equal(t, []any{"STOP"}, decoded["stopSequences"])
	assert.Equal(t, 0.4, decoded["temperature"])
	assert.Equal(t, 0.9, decoded["topP"])
	assert.Contains(t, decoded, "thinkingConfig")
}

func TestStopReasonMapsFinishReasons(t *testing.T) {
	assert.Equal(t, "max_tokens", antigravity.StopReason("MAX_TOKENS"))
	assert.Equal(t, "refusal", antigravity.StopReason("SAFETY"))
	assert.Equal(t, "end_turn", antigravity.StopReason("STOP"))
	assert.Equal(t, "end_turn", antigravity.StopReason(""))
}
