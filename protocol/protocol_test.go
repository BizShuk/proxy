package protocol

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequestMeta(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{name: "chat", body: `{"model":"gpt-4o","stream":true}`, wantModel: "gpt-4o", wantStream: true},
		{name: "responses omitted stream", body: `{"model":"gpt-5","input":"hi"}`, wantModel: "gpt-5"},
		{name: "missing model", body: `{"stream":true}`, wantErr: true},
		{name: "blank model", body: `{"model":" "}`, wantErr: true},
		{name: "malformed", body: `{`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, stream, err := ParseRequestMeta([]byte(tc.body))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantModel, model)
			assert.Equal(t, tc.wantStream, stream)
		})
	}
}

func TestEncodeErrorUsesSourceFormat(t *testing.T) {
	proxyErr := &ProxyError{
		Kind: ERROR_RATE_LIMIT, Status: http.StatusTooManyRequests,
		Code: "rate_limit_exceeded", Message: "slow down",
	}

	anthropicBody, err := EncodeError(FORMAT_ANTHROPIC_MESSAGES, proxyErr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, string(anthropicBody))

	for _, format := range []Format{FORMAT_OPENAI_CHAT, FORMAT_OPENAI_RESPONSES} {
		body, encodeErr := EncodeError(format, proxyErr)
		require.NoError(t, encodeErr)
		assert.JSONEq(t, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`, string(body))
	}
}

func TestProxyErrorUnwrapsCauseAndDefaultsStatus(t *testing.T) {
	cause := errors.New("decode failed")
	err := &ProxyError{Kind: ERROR_INVALID_REQUEST, Message: "bad input", Cause: cause}
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, http.StatusBadRequest, err.StatusCode())
	assert.Equal(t, "bad input", err.Error())
}

func TestProxyErrorMetadata(t *testing.T) {
	err := &ProxyError{
		Kind: ERROR_RATE_LIMIT, Status: http.StatusTooManyRequests,
		RetryAfter: 7 * time.Second, UpstreamRequestID: "req_1",
	}
	assert.Equal(t, 7*time.Second, err.RetryAfter)
	assert.Equal(t, "req_1", err.UpstreamRequestID)
}
