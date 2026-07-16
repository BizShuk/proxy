package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Format identifies one supported LLM wire protocol.
type Format string

const (
	FORMAT_ANTHROPIC_MESSAGES Format = "anthropic-messages"
	FORMAT_OPENAI_CHAT        Format = "openai-chat"
	FORMAT_OPENAI_RESPONSES   Format = "openai-responses"
)

// ALL_FORMATS is the complete supported protocol set.
var ALL_FORMATS = []Format{
	FORMAT_ANTHROPIC_MESSAGES,
	FORMAT_OPENAI_CHAT,
	FORMAT_OPENAI_RESPONSES,
}

// Valid reports whether the format belongs to ALL_FORMATS.
func (f Format) Valid() bool {
	for _, candidate := range ALL_FORMATS {
		if f == candidate {
			return true
		}
	}
	return false
}

// ParseRequestMeta extracts routing metadata without committing to a protocol DTO.
func ParseRequestMeta(body []byte) (string, bool, error) {
	var meta struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream"`
	}
	if err := json.Unmarshal(body, &meta); err != nil {
		return "", false, fmt.Errorf("decode request metadata: %w", err)
	}
	meta.Model = strings.TrimSpace(meta.Model)
	if meta.Model == "" {
		return "", false, fmt.Errorf("decode request metadata: model must not be blank")
	}
	return meta.Model, meta.Stream != nil && *meta.Stream, nil
}
