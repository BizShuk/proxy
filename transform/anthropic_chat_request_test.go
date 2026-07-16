package transform

import (
	"context"
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicChatRequestTransforms(t *testing.T) {
	tests := []struct {
		name       string
		fn         RequestTransform
		in         string
		want       string
		lossFields []string
	}{
		{
			name:       "anthropic to chat",
			fn:         AnthropicToChatRequest,
			in:         "anthropic_request_full.json",
			want:       "chat_from_anthropic.json",
			lossFields: []string{"thinking.budget_tokens"},
		},
		{
			name:       "chat to anthropic",
			fn:         ChatToAnthropicRequest,
			in:         "chat_request_full.json",
			want:       "anthropic_from_chat.json",
			lossFields: []string{"messages.role", "messages.name", "reasoning_effort"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := mustFixture(t, tc.in)
			want := mustFixture(t, tc.want)
			got, err := tc.fn(context.Background(), protocol.RequestEnvelope{
				Model:  "target-model",
				Stream: true,
				Body:   in,
			})
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got.Body))
			require.Len(t, got.Losses, len(tc.lossFields))
			for index, field := range tc.lossFields {
				assert.Equal(t, field, got.Losses[index].Field)
			}
		})
	}
}

func TestAnthropicToChatRejectsUnsupportedContentBlock(t *testing.T) {
	body := []byte(`{"model":"claude","messages":[{"role":"user","content":[{"type":"document"}]}]}`)

	_, err := AnthropicToChatRequest(context.Background(), protocol.RequestEnvelope{Model: "gpt", Body: body})

	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}

func TestChatToAnthropicRejectsRemoteImage(t *testing.T) {
	body := []byte(`{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`)

	_, err := ChatToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})

	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}

func TestChatToAnthropicRejectsUnsupportedContentPart(t *testing.T) {
	body := []byte(`{"model":"gpt","messages":[{"role":"user","content":[{"type":"input_audio"}]}]}`)

	_, err := ChatToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})

	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}
