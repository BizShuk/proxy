package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/antigravity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAntigravitySignatureCacheEvictsOldest(t *testing.T) {
	cache := newAntigravitySignatureCache(2)
	cache.Remember("a", "sig-a")
	cache.Remember("b", "sig-b")
	cache.Remember("c", "sig-c")

	assert.Empty(t, cache.Lookup("a"))
	assert.Equal(t, "sig-b", cache.Lookup("b"))
	assert.Equal(t, "sig-c", cache.Lookup("c"))
}

func TestAntigravitySignatureCacheIgnoresBlanks(t *testing.T) {
	cache := newAntigravitySignatureCache(4)
	cache.Remember("", "sig")
	cache.Remember("id", "")

	assert.Empty(t, cache.Lookup("id"))
	assert.Empty(t, cache.Lookup(""))
}

// Re-storing a known call must refresh it in place rather than consume another
// slot, or a long tool loop would evict itself.
func TestAntigravitySignatureCacheUpdatesInPlace(t *testing.T) {
	cache := newAntigravitySignatureCache(2)
	cache.Remember("a", "sig-a")
	cache.Remember("a", "sig-a2")
	cache.Remember("b", "sig-b")

	assert.Equal(t, "sig-a2", cache.Lookup("a"))
	assert.Equal(t, "sig-b", cache.Lookup("b"))
}

// The response must hand the signature to the client on the tool_use block, so
// clients that round-trip unknown fields need no proxy-side state at all.
func TestAntigravityResponseExposesToolSignature(t *testing.T) {
	result, err := AntigravityToAnthropicResponse(context.Background(), model.ResponseEnvelope{
		Status: 200,
		Body: []byte(`{"response":{"candidates":[{"content":{"parts":[
			{"functionCall":{"id":"call_sig_1","name":"Bash","args":{"cmd":"ls"}},"thoughtSignature":"sig-xyz"}
		]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}}`),
		Exchange: model.Exchange{OriginalRequest: model.RequestEnvelope{Model: "gemini-3.6-flash-high"}},
	})
	require.NoError(t, err)

	decoded, err := anthropic.DecodeResponse(result.Body)
	require.NoError(t, err)
	require.Len(t, decoded.Content, 1)
	assert.Equal(t, "sig-xyz", decoded.Content[0].Signature)
}

// The real failure mode: Gemini 3 rejects a replayed functionCall with no
// thought signature, and Anthropic clients drop the non-standard field. The
// cache has to reattach it from the turn that issued the call.
func TestAntigravitySignatureSurvivesLossyClientRoundTrip(t *testing.T) {
	const callID = "call_roundtrip_1"

	_, err := AntigravityToAnthropicResponse(context.Background(), model.ResponseEnvelope{
		Status: 200,
		Body: []byte(fmt.Sprintf(`{"response":{"candidates":[{"content":{"parts":[
			{"functionCall":{"id":%q,"name":"Bash","args":{"cmd":"ls"}},"thoughtSignature":"sig-replay"}
		]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}}`, callID)),
		Exchange: model.Exchange{OriginalRequest: model.RequestEnvelope{Model: "gemini-3.6-flash-high"}},
	})
	require.NoError(t, err)

	// The client echoes the tool_use back with the signature field stripped.
	replay, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		Model: "gemini-3.6-flash-high",
		Body: []byte(fmt.Sprintf(`{
			"model": "gemini-3.6-flash-high", "max_tokens": 64,
			"messages": [
				{"role": "user", "content": [{"type": "text", "text": "list files"}]},
				{"role": "assistant", "content": [
					{"type": "tool_use", "id": %q, "name": "Bash", "input": {"cmd": "ls"}}
				]},
				{"role": "user", "content": [
					{"type": "tool_result", "tool_use_id": %q, "content": "a.txt"}
				]}
			]
		}`, callID, callID)),
	})
	require.NoError(t, err)

	var decoded antigravity.Request
	require.NoError(t, json.Unmarshal(replay.Body, &decoded))
	call := decoded.Request.Contents[1].Parts[0]
	require.NotNil(t, call.FunctionCall)
	assert.Equal(t, "sig-replay", call.ThoughtSignature)
}

// The streaming path issues the same tool calls, so it must populate the cache
// too — otherwise every streamed tool loop breaks on its second turn.
func TestAntigravityStreamRecordsToolSignature(t *testing.T) {
	const callID = "call_stream_sig_1"

	stream, err := NewAntigravityToAnthropicStream(model.Exchange{
		OriginalRequest: model.RequestEnvelope{Model: "gemini-3.6-flash-high"},
	})
	require.NoError(t, err)

	out, err := stream.Push(context.Background(), model.SSEFrame{
		Data: []byte(fmt.Sprintf(`{"response":{"responseId":"resp_sig","candidates":[{"content":{"parts":[
			{"functionCall":{"id":%q,"name":"Bash","args":{"cmd":"ls"}},"thoughtSignature":"sig-stream"}
		]}}]}}`, callID)),
	})
	require.NoError(t, err)

	var started string
	for _, frame := range out {
		if frame.Event == "content_block_start" {
			started = string(frame.Data)
		}
	}
	assert.Contains(t, started, "sig-stream")
	assert.Equal(t, "sig-stream", antigravityToolSignatures.Lookup(callID))
}

// A tool call the proxy never saw has no signature to replay; the request still
// goes out, but the loss must be reported rather than hidden.
func TestAntigravityRequestReportsMissingToolSignature(t *testing.T) {
	result, err := AnthropicToAntigravityRequest(context.Background(), model.RequestEnvelope{
		Model: "gemini-3.6-flash-high",
		Body: []byte(`{
			"model": "gemini-3.6-flash-high", "max_tokens": 64,
			"messages": [{"role": "assistant", "content": [
				{"type": "tool_use", "id": "call_never_seen", "name": "Bash", "input": {}}
			]}]
		}`),
	})
	require.NoError(t, err)

	found := false
	for _, loss := range result.Losses {
		if loss.Reason != "" && loss.Field == "messages[0].content[0].signature" {
			found = true
		}
	}
	assert.True(t, found, "missing thought signature must be reported as a loss")
}
