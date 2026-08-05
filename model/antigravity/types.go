// Package antigravity adapts agentsdk's Antigravity wire contract for the
// proxy's wire-to-wire transforms.
//
// agentsdk owns the protocol: endpoints, headers, host fallback, project
// discovery, the Cloud Code envelope and the Google-dialect schema conversion
// all live in agentsdk/provider/antigravity, and the types below are aliases
// onto it rather than a second definition.
//
// Two containers are still declared here. agentsdk's request types are shaped
// for core.ModelRequest, which carries no sampling controls at all, so its
// GenerationConfig has no temperature or topP field and its GenerateRequest
// cannot hold one that does. The gateway accepts both — every reference client
// sends them — so dropping them would silently discard what an Anthropic
// caller asked for. Only these two structs are restated; every leaf type is
// agentsdk's.
package antigravity

import (
	"encoding/json"
	"fmt"
	"strings"

	ag "github.com/bizshuk/agentsdk/provider/antigravity"
)

// Wire vocabulary owned by agentsdk.
type (
	Content               = ag.Content
	Part                  = ag.Part
	InlineData            = ag.InlineData
	FunctionCall          = ag.FunctionCall
	FunctionResponse      = ag.FunctionResponse
	Tool                  = ag.Tool
	FunctionDeclaration   = ag.FunctionDeclaration
	ToolConfig            = ag.ToolConfig
	FunctionCallingConfig = ag.FunctionCallingConfig
	ClaudeThinkingConfig  = ag.ClaudeThinkingConfig
	GeminiThinkingConfig  = ag.GeminiThinkingConfig
	Candidate             = ag.Candidate
	UsageMetadata         = ag.UsageMetadata
)

const (
	// USER_AGENT is the envelope's client marker.
	USER_AGENT = ag.CLIENT_NAME
	// REQUEST_TYPE_AGENT is the envelope request class for chat traffic.
	REQUEST_TYPE_AGENT = "agent"

	// MODE_AUTO lets the model decide whether to call a tool.
	MODE_AUTO = "AUTO"
	// MODE_NONE forbids tool calls.
	MODE_NONE = "NONE"
	// MODE_ANY forces a tool call.
	MODE_ANY = "ANY"
	// MODE_VALIDATED validates arguments upstream; the gateway's Claude models
	// require it and do not default to it.
	MODE_VALIDATED = "VALIDATED"

	// ROLE_USER is the Gemini caller role; Anthropic "system" folds into it.
	ROLE_USER = "user"
	// ROLE_MODEL is the Gemini assistant role.
	ROLE_MODEL = "model"
)

// GenerationConfig extends agentsdk's with the sampling controls an Anthropic
// caller can set. The embedded struct supplies maxOutputTokens, stopSequences
// and thinkingConfig; encoding/json flattens it into one JSON object.
type GenerationConfig struct {
	ag.GenerationConfig
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"topP,omitempty"`
}

// Inner is the Gemini GenerateContent payload inside the envelope. It mirrors
// agentsdk's GenerateRequest except for the widened GenerationConfig.
type Inner struct {
	Contents          []Content         `json:"contents"`
	SystemInstruction *Content          `json:"systemInstruction,omitempty"`
	Tools             []Tool            `json:"tools,omitempty"`
	ToolConfig        *ToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *GenerationConfig `json:"generationConfig,omitempty"`
	SessionID         string            `json:"sessionId,omitempty"`
}

// Request is the Cloud Code v1internal envelope.
type Request struct {
	Model       string `json:"model"`
	Project     string `json:"project,omitempty"`
	UserAgent   string `json:"userAgent,omitempty"`
	RequestType string `json:"requestType,omitempty"`
	RequestID   string `json:"requestId,omitempty"`
	Request     Inner  `json:"request"`
}

// Body is one decoded response payload. agentsdk's GenerateResponse carries
// the candidates and usage; the identity fields alongside it are read here
// because the proxy echoes them back to the caller as the message id and model.
type Body struct {
	ag.GenerateResponse
	ResponseID   string `json:"responseId,omitempty"`
	ModelVersion string `json:"modelVersion,omitempty"`
	// CachedTokenCount is not part of agentsdk's UsageMetadata; Anthropic
	// reports cached input separately from fresh input, so it is decoded
	// alongside rather than lost.
	CachedTokenCount int `json:"-"`
}

// SanitizeSchema converts one JSON Schema into the Google dialect the gateway
// validates tool parameters against.
func SanitizeSchema(raw json.RawMessage) json.RawMessage {
	fallback := json.RawMessage(`{"type":"OBJECT","properties":{"reason":{"type":"STRING"}}}`)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return fallback
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fallback
	}
	encoded, err := json.Marshal(ag.CleanSchema(withExplicitProperties(decoded)))
	if err != nil {
		return fallback
	}
	return encoded
}

// withExplicitProperties gives an object schema an empty properties map when it
// declares none at all.
//
// agentsdk substitutes a placeholder for `properties: {}` but not for a missing
// key, and Google rejects a property-less OBJECT either way. A zero-argument
// tool is commonly written as bare `{"type": "object"}`, so normalizing here
// lets agentsdk's own placeholder rule fire instead of restating it.
func withExplicitProperties(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	name, _ := schema["type"].(string)
	if !strings.EqualFold(name, "object") {
		return value
	}
	if properties, exists := schema["properties"]; exists {
		if _, isObject := properties.(map[string]any); isObject {
			return value
		}
	}
	normalized := make(map[string]any, len(schema)+1)
	for key, entry := range schema {
		normalized[key] = entry
	}
	normalized["properties"] = map[string]any{}
	return normalized
}

// DecodeResponse decodes one response envelope or stream frame, accepting both
// the wrapped and the bare shape.
func DecodeResponse(raw []byte) (*Body, error) {
	unwrapped, err := ag.Unwrap(raw)
	if err != nil {
		return nil, fmt.Errorf("decode antigravity response: %w", err)
	}
	body := &Body{GenerateResponse: unwrapped}

	// Identity and cached-token fields sit outside agentsdk's response type,
	// and may be at either nesting level depending on which shape arrived.
	var extra struct {
		Response *struct {
			ResponseID    string `json:"responseId"`
			ModelVersion  string `json:"modelVersion"`
			UsageMetadata *struct {
				CachedContentTokenCount int `json:"cachedContentTokenCount"`
			} `json:"usageMetadata"`
		} `json:"response"`
		ResponseID    string `json:"responseId"`
		ModelVersion  string `json:"modelVersion"`
		UsageMetadata *struct {
			CachedContentTokenCount int `json:"cachedContentTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.Unmarshal(raw, &extra); err == nil {
		body.ResponseID, body.ModelVersion = extra.ResponseID, extra.ModelVersion
		if extra.UsageMetadata != nil {
			body.CachedTokenCount = extra.UsageMetadata.CachedContentTokenCount
		}
		if extra.Response != nil {
			body.ResponseID = extra.Response.ResponseID
			body.ModelVersion = extra.Response.ModelVersion
			if extra.Response.UsageMetadata != nil {
				body.CachedTokenCount = extra.Response.UsageMetadata.CachedContentTokenCount
			}
		}
	}
	return body, nil
}

// Encode serializes an Antigravity wire value.
func Encode(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode antigravity value: %w", err)
	}
	return body, nil
}

// StopReason maps a Gemini finish reason onto its Anthropic equivalent.
// Tool use is not derivable from the finish reason alone: the gateway reports
// STOP for a turn that ends in a functionCall, so callers that saw a tool part
// must override this.
func StopReason(finishReason string) string {
	switch strings.ToUpper(strings.TrimSpace(finishReason)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return "refusal"
	default:
		return "end_turn"
	}
}
