package transform

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bizshuk/proxy/model/responses"
)

const (
	RESPONSES_REASONING_SIGNATURE_V1_PREFIX = "proxy-responses-reasoning-v1:"
	RESPONSES_REASONING_SIGNATURE_V2_PREFIX = "proxy-responses-reasoning-v2:"
)

type responsesReasoningSignature struct {
	ID               string                 `json:"id,omitempty"`
	Summary          *responses.ContentList `json:"summary,omitempty"`
	Content          *responses.ContentList `json:"content,omitempty"`
	EncryptedContent string                 `json:"encrypted_content,omitempty"`
	Status           string                 `json:"status,omitempty"`
}

func encodeResponsesReasoningSignature(metadata responsesReasoningSignature) (string, error) {
	if metadata.ID == "" && metadata.Summary == nil && metadata.Content == nil &&
		metadata.EncryptedContent == "" && metadata.Status == "" {
		return "", nil
	}
	body, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode reasoning signature payload: %w", err)
	}
	return RESPONSES_REASONING_SIGNATURE_V2_PREFIX + base64.RawURLEncoding.EncodeToString(body), nil
}

func decodeResponsesReasoningSignature(signature string) (responsesReasoningSignature, bool, error) {
	prefix := ""
	switch {
	case strings.HasPrefix(signature, RESPONSES_REASONING_SIGNATURE_V2_PREFIX):
		prefix = RESPONSES_REASONING_SIGNATURE_V2_PREFIX
	case strings.HasPrefix(signature, RESPONSES_REASONING_SIGNATURE_V1_PREFIX):
		prefix = RESPONSES_REASONING_SIGNATURE_V1_PREFIX
	default:
		return responsesReasoningSignature{}, false, nil
	}
	encoded := strings.TrimPrefix(signature, prefix)
	body, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return responsesReasoningSignature{}, true, fmt.Errorf("decode reasoning signature: %w", err)
	}
	if prefix == RESPONSES_REASONING_SIGNATURE_V1_PREFIX {
		var legacy struct {
			ID               string `json:"id,omitempty"`
			EncryptedContent string `json:"encrypted_content,omitempty"`
		}
		if err := json.Unmarshal(body, &legacy); err != nil {
			return responsesReasoningSignature{}, true, fmt.Errorf("decode reasoning signature payload: %w", err)
		}
		return responsesReasoningSignature{
			ID: legacy.ID, EncryptedContent: legacy.EncryptedContent,
		}, true, nil
	}
	var metadata responsesReasoningSignature
	if err := json.Unmarshal(body, &metadata); err != nil {
		return responsesReasoningSignature{}, true, fmt.Errorf("decode reasoning signature payload: %w", err)
	}
	return metadata, true, nil
}

func responsesReasoningSignatureFromOutputItem(item responses.OutputItem) responsesReasoningSignature {
	metadata := responsesReasoningSignature{
		ID: item.ID, EncryptedContent: item.EncryptedContent, Status: item.Status,
	}
	if item.Summary != nil {
		summary := cloneResponsesContentList(item.Summary)
		metadata.Summary = &summary
	}
	if item.Content != nil {
		content := cloneResponsesContentList(item.Content)
		metadata.Content = &content
	}
	return metadata
}

func cloneResponsesContentList(source responses.ContentList) responses.ContentList {
	if source == nil {
		return nil
	}
	cloned := make(responses.ContentList, len(source))
	copy(cloned, source)
	return cloned
}
