package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const RESPONSES_REASONING_SIGNATURE_PREFIX = "proxy-responses-reasoning-v1:"

type responsesReasoningSignature struct {
	ID               string `json:"id,omitempty"`
	EncryptedContent string `json:"encrypted_content,omitempty"`
}

func encodeResponsesReasoningSignature(metadata responsesReasoningSignature) (string, error) {
	if metadata.ID == "" && metadata.EncryptedContent == "" {
		return "", nil
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode reasoning signature payload: %w", err)
	}
	return RESPONSES_REASONING_SIGNATURE_PREFIX + base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeResponsesReasoningSignature(signature string) (responsesReasoningSignature, bool, error) {
	if !strings.HasPrefix(signature, RESPONSES_REASONING_SIGNATURE_PREFIX) {
		return responsesReasoningSignature{}, false, nil
	}
	encoded := strings.TrimPrefix(signature, RESPONSES_REASONING_SIGNATURE_PREFIX)
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return responsesReasoningSignature{}, true, fmt.Errorf("decode reasoning signature: %w", err)
	}
	var metadata responsesReasoningSignature
	if err := json.Unmarshal(body, &metadata); err != nil {
		return responsesReasoningSignature{}, true, fmt.Errorf("decode reasoning signature payload: %w", err)
	}
	return metadata, true, nil
}
