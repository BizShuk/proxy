package transform

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeResponsesReasoningSignatureUsesV2(t *testing.T) {
	signature, err := encodeResponsesReasoningSignature(responsesReasoningSignature{
		ID: "reasoning_1", EncryptedContent: "encrypted-reasoning",
	})
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(signature, "proxy-responses-reasoning-v2:"))
}

func TestDecodeResponsesReasoningSignatureAcceptsV1(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte(
		`{"id":"reasoning_v1","encrypted_content":"encrypted-v1"}`,
	))

	metadata, recognized, err := decodeResponsesReasoningSignature(
		"proxy-responses-reasoning-v1:" + body,
	)
	require.NoError(t, err)
	require.True(t, recognized)
	assert.Equal(t, "reasoning_v1", metadata.ID)
	assert.Equal(t, "encrypted-v1", metadata.EncryptedContent)
}
