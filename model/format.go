package model

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Format identifies one supported LLM wire model.
type Format string

const (
	FORMAT_ANTHROPIC_MESSAGES Format = "anthropic-messages"
	FORMAT_OPENAI_CHAT        Format = "openai-chat"
	FORMAT_OPENAI_RESPONSES   Format = "openai-responses"
	FORMAT_ANTIGRAVITY        Format = "antigravity"
)

// CLIENT_FORMATS are the formats the proxy both accepts from callers and can
// emit upstream. Every ordered pair of these must exist in the transform matrix.
var CLIENT_FORMATS = []Format{
	FORMAT_ANTHROPIC_MESSAGES,
	FORMAT_OPENAI_CHAT,
	FORMAT_OPENAI_RESPONSES,
}

// PROVIDER_FORMATS are upstream-only wire formats. They are legal transform
// targets but never a client-facing source, so no public endpoint decodes them
// and the matrix does not require a full row for them.
var PROVIDER_FORMATS = []Format{
	FORMAT_ANTIGRAVITY,
}

// ALL_FORMATS is the complete supported protocol set.
var ALL_FORMATS = append(append([]Format{}, CLIENT_FORMATS...), PROVIDER_FORMATS...)

// Valid reports whether the format belongs to ALL_FORMATS.
func (f Format) Valid() bool {
	for _, candidate := range ALL_FORMATS {
		if f == candidate {
			return true
		}
	}
	return false
}

// ClientFacing reports whether a public endpoint may accept this format.
func (f Format) ClientFacing() bool {
	for _, candidate := range CLIENT_FORMATS {
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
