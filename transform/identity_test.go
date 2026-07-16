package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityPairsNormalizeModelWithoutSemanticChange(t *testing.T) {
	tests := []struct {
		name string
		pair Pair
		body string
		want string
	}{
		{name: "anthropic", pair: AnthropicIdentity(), body: `{"model":"route/claude","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, want: `{"model":"actual-model","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`},
		{name: "chat", pair: ChatIdentity(), body: `{"model":"route/gpt","messages":[{"role":"user","content":"hi"}]}`, want: `{"model":"actual-model","messages":[{"role":"user","content":"hi"}]}`},
		{name: "responses", pair: ResponsesIdentity(), body: `{"model":"route/gpt","input":"hi"}`, want: `{"model":"actual-model","input":"hi"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.pair.Request(context.Background(), protocol.RequestEnvelope{Model: "actual-model", Body: []byte(tc.body)})
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(result.Body))
		})
	}
}

func TestIdentityStreamForwardsFullFrameAndRequiresTerminal(t *testing.T) {
	retry := 10
	tests := []struct {
		name     string
		pair     Pair
		terminal protocol.SSEFrame
	}{
		{name: "anthropic", pair: AnthropicIdentity(), terminal: protocol.SSEFrame{Event: "message_stop", ID: "1", RetryMillis: &retry, Comments: []string{"keep"}, Data: []byte(`{"type":"message_stop"}`)}},
		{name: "chat", pair: ChatIdentity(), terminal: protocol.SSEFrame{Data: []byte(`[DONE]`)}},
		{name: "responses", pair: ResponsesIdentity(), terminal: protocol.SSEFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"status":"completed"}}`)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stream, err := tc.pair.NewStream(protocol.Exchange{})
			require.NoError(t, err)
			_, err = stream.Close(context.Background())
			require.Error(t, err)

			stream, err = tc.pair.NewStream(protocol.Exchange{})
			require.NoError(t, err)
			frames, err := stream.Push(context.Background(), tc.terminal)
			require.NoError(t, err)
			require.Equal(t, []protocol.SSEFrame{tc.terminal}, frames)
			_, err = stream.Close(context.Background())
			require.NoError(t, err)
		})
	}
}

func TestIdentityStreamAcceptsFailureTerminal(t *testing.T) {
	tests := []struct {
		pair  Pair
		frame protocol.SSEFrame
	}{
		{pair: AnthropicIdentity(), frame: protocol.SSEFrame{Event: "error", Data: []byte(`{"type":"error","error":{"message":"failed"}}`)}},
		{pair: ResponsesIdentity(), frame: protocol.SSEFrame{Event: "response.failed", Data: []byte(`{"type":"response.failed","response":{"status":"failed"}}`)}},
	}
	for _, tc := range tests {
		stream, err := tc.pair.NewStream(protocol.Exchange{})
		require.NoError(t, err)
		_, err = stream.Push(context.Background(), tc.frame)
		require.NoError(t, err)
		_, err = stream.Close(context.Background())
		require.NoError(t, err)
	}
}
