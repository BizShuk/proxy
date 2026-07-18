// Package upstream defines concrete provider profiles and upstream transport metadata.
package upstream

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/bizshuk/agentsdk/auth/auth"
	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/chat"
	"github.com/bizshuk/proxy/protocol/responses"
	"github.com/bizshuk/proxy/route"
)

const (
	// ANTHROPIC_OAUTH_BETA is required for Anthropic OAuth requests.
	ANTHROPIC_OAUTH_BETA = "oauth-2025-04-20"
	// DEFAULT_CODEX_ORIGINATOR identifies requests made through the Codex profile.
	DEFAULT_CODEX_ORIGINATOR = "codex_cli_rs"
	// DEFAULT_CODEX_VERSION is the Codex compatibility version sent upstream.
	DEFAULT_CODEX_VERSION = "0.125.0"

	ANTHROPIC_VERSION = "2023-06-01"
)

// AuthScheme identifies the provider's default credential header.
type AuthScheme string

const (
	AUTH_X_API_KEY AuthScheme = "x-api-key"
	AUTH_BEARER    AuthScheme = "bearer"
)

// NormalizedRequest contains provider-specific request mutations.
type NormalizedRequest struct {
	Body              []byte
	UpstreamStream    bool
	BridgeToNonStream bool
}

// NormalizeRequest applies provider-specific requirements after protocol transforms.
type NormalizeRequest func(protocol.RequestEnvelope) (NormalizedRequest, error)

// Profile describes one concrete upstream API surface.
type Profile struct {
	ID                             string
	Routing                        route.Profile
	CredentialProvider             string
	BaseURL                        string
	Endpoints                      map[protocol.Format]string
	Preferred                      protocol.Format
	AuthScheme                     AuthScheme
	AllowedRequestHeaders          []string
	AllowedResponseHeaders         []string
	AdvertisedModels               []string
	AnthropicVersion               string
	CountTokensEndpoint            string
	AllowsMissingStreamContentType bool
	NormalizeRequest               NormalizeRequest
}

// ResolveEndpoint returns the fixed endpoint for a supported format.
func (p Profile) ResolveEndpoint(format protocol.Format) (string, error) {
	endpoint, ok := p.Endpoints[format]
	if !ok {
		return "", unsupportedFormatError(p.ID, format)
	}
	return endpoint, nil
}

// AllowsRequestHeader reports whether a downstream request header may be forwarded.
func (p Profile) AllowsRequestHeader(name string) bool {
	return headerAllowed(name, p.AllowedRequestHeaders)
}

// AllowsResponseHeader reports whether an upstream response header may be returned.
func (p Profile) AllowsResponseHeader(name string) bool {
	return headerAllowed(name, p.AllowedResponseHeaders)
}

// Catalog is an immutable registry of concrete upstream profiles.
type Catalog struct {
	profiles map[string]Profile
}

// NewCatalog validates and copies concrete provider profiles.
func NewCatalog(profiles []Profile) (*Catalog, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("upstream catalog: no profiles")
	}
	catalog := &Catalog{profiles: make(map[string]Profile, len(profiles))}
	for index, source := range profiles {
		profile, err := normalizeConcreteProfile(source)
		if err != nil {
			return nil, fmt.Errorf("upstream catalog profile %d: %w", index, err)
		}
		if _, exists := catalog.profiles[profile.ID]; exists {
			return nil, fmt.Errorf("upstream catalog: duplicate profile %q", profile.ID)
		}
		catalog.profiles[profile.ID] = profile
	}
	return catalog, nil
}

// Lookup returns an independent copy of a concrete profile.
func (c *Catalog) Lookup(id string) (Profile, bool) {
	if c == nil {
		return Profile{}, false
	}
	profile, ok := c.profiles[strings.ToLower(strings.TrimSpace(id))]
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(profile), true
}

// AdvertisedModels returns the catalog's unique model identifiers and prefixes.
func (c *Catalog) AdvertisedModels() []string {
	if c == nil {
		return nil
	}
	unique := make(map[string]struct{})
	for _, profile := range c.profiles {
		for _, model := range profile.AdvertisedModels {
			model = strings.TrimSpace(model)
			if model != "" {
				unique[model] = struct{}{}
			}
		}
	}
	models := make([]string, 0, len(unique))
	for model := range unique {
		models = append(models, model)
	}
	slices.Sort(models)
	return models
}

// NewRouter creates a deterministic router from the catalog's provider families.
func (c *Catalog) NewRouter() (*route.Router, error) {
	if c == nil {
		return nil, fmt.Errorf("upstream catalog: nil catalog")
	}
	families := make(map[string]route.Profile)
	for _, profile := range c.profiles {
		familyID := strings.ToLower(strings.TrimSpace(profile.Routing.ID))
		if existing, exists := families[familyID]; exists {
			if !routingProfilesEqual(existing, profile.Routing) {
				return nil, fmt.Errorf("upstream catalog: inconsistent routing profile %q", familyID)
			}
			continue
		}
		families[familyID] = profile.Routing
	}
	profiles := make([]route.Profile, 0, len(families))
	for _, profile := range families {
		profiles = append(profiles, profile)
	}
	slices.SortFunc(profiles, func(left, right route.Profile) int {
		return strings.Compare(left.ID, right.ID)
	})
	return route.NewRouter(profiles)
}

// ResolveProfile selects a concrete API profile and validates the target format.
func (c *Catalog) ResolveProfile(providerFamily string, credentialKind auth.Kind, forcedTarget *protocol.Format) (Profile, protocol.Format, error) {
	family := strings.ToLower(strings.TrimSpace(providerFamily))
	profileID := family
	if family == "openai" {
		switch credentialKind {
		case auth.KIND_API_KEY:
			profileID = "openai-api"
		case auth.KIND_OAUTH:
			profileID = "openai-codex-oauth"
		default:
			return Profile{}, "", unsupportedCredentialError(family, credentialKind)
		}
	}
	profile, ok := c.Lookup(profileID)
	if !ok {
		return Profile{}, "", &protocol.ProxyError{
			Kind:    protocol.ERROR_UNKNOWN_MODEL,
			Status:  http.StatusBadRequest,
			Code:    "unknown_provider",
			Message: fmt.Sprintf("unknown provider family %q", providerFamily),
		}
	}

	target := profile.Preferred
	if forcedTarget != nil {
		target = *forcedTarget
	}
	if _, ok := profile.Endpoints[target]; !ok {
		return Profile{}, "", unsupportedFormatError(profile.ID, target)
	}
	return profile, target, nil
}

// DefaultCatalog constructs the production provider catalog.
func DefaultCatalog() (*Catalog, error) {
	defaultRequestHeaders := []string{"x-request-id", "traceparent", "tracestate"}
	defaultResponseHeaders := []string{"content-type", "retry-after", "x-request-id", "request-id", "cf-ray"}
	profiles := []Profile{
		{
			ID:                     "anthropic",
			Routing:                route.Profile{ID: "anthropic", Qualifiers: []string{"anthropic"}, Prefixes: []string{"claude-"}},
			CredentialProvider:     "anthropic",
			BaseURL:                "https://api.anthropic.com",
			Endpoints:              map[protocol.Format]string{protocol.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"},
			Preferred:              protocol.FORMAT_ANTHROPIC_MESSAGES,
			AuthScheme:             AUTH_X_API_KEY,
			AllowedRequestHeaders:  append(slices.Clone(defaultRequestHeaders), "anthropic-beta"),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"claude-"},
			AnthropicVersion:       ANTHROPIC_VERSION,
			CountTokensEndpoint:    "/v1/messages/count_tokens",
			NormalizeRequest:       preserveRequest,
		},
		{
			ID:                     "minimax",
			Routing:                route.Profile{ID: "minimax", Qualifiers: []string{"minimax"}, ExactModels: []string{"MiniMax-Text-01"}, Prefixes: []string{"minimax-"}},
			CredentialProvider:     "minimax",
			BaseURL:                "https://api.minimax.io/anthropic",
			Endpoints:              map[protocol.Format]string{protocol.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"},
			Preferred:              protocol.FORMAT_ANTHROPIC_MESSAGES,
			AuthScheme:             AUTH_X_API_KEY,
			AllowedRequestHeaders:  slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"MiniMax-Text-01", "minimax-"},
			NormalizeRequest:       preserveRequest,
		},
		{
			ID:                 "openai-api",
			Routing:            route.Profile{ID: "openai", Qualifiers: []string{"openai", "openai-chat"}, Prefixes: []string{"gpt-", "o1-", "o3-"}},
			CredentialProvider: "openai",
			BaseURL:            "https://api.openai.com",
			Endpoints: map[protocol.Format]string{
				protocol.FORMAT_OPENAI_RESPONSES: "/v1/responses",
				protocol.FORMAT_OPENAI_CHAT:      "/v1/chat/completions",
			},
			Preferred:              protocol.FORMAT_OPENAI_RESPONSES,
			AuthScheme:             AUTH_BEARER,
			AllowedRequestHeaders:  slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"gpt-", "o1-", "o3-"},
			NormalizeRequest:       preserveRequest,
		},
		{
			ID:                             "openai-codex-oauth",
			Routing:                        route.Profile{ID: "openai", Qualifiers: []string{"openai", "openai-chat"}, Prefixes: []string{"gpt-", "o1-", "o3-"}},
			CredentialProvider:             "openai",
			BaseURL:                        "https://chatgpt.com/backend-api",
			Endpoints:                      map[protocol.Format]string{protocol.FORMAT_OPENAI_RESPONSES: "/codex/responses"},
			Preferred:                      protocol.FORMAT_OPENAI_RESPONSES,
			AuthScheme:                     AUTH_BEARER,
			AllowedRequestHeaders:          slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders:         slices.Clone(defaultResponseHeaders),
			AdvertisedModels:               []string{"gpt-", "o1-", "o3-"},
			AllowsMissingStreamContentType: true,
			NormalizeRequest:               normalizeCodexRequest,
		},
		{
			ID:                 "xai",
			Routing:            route.Profile{ID: "xai", Qualifiers: []string{"xai", "xai-chat"}, Prefixes: []string{"grok-"}},
			CredentialProvider: "xai",
			BaseURL:            "https://api.x.ai",
			Endpoints: map[protocol.Format]string{
				protocol.FORMAT_OPENAI_RESPONSES: "/v1/responses",
				protocol.FORMAT_OPENAI_CHAT:      "/v1/chat/completions",
			},
			Preferred:              protocol.FORMAT_OPENAI_RESPONSES,
			AuthScheme:             AUTH_BEARER,
			AllowedRequestHeaders:  slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"grok-"},
			NormalizeRequest:       normalizeXAIRequest,
		},
	}
	return NewCatalog(profiles)
}

func preserveRequest(envelope protocol.RequestEnvelope) (NormalizedRequest, error) {
	return NormalizedRequest{
		Body:           envelope.Body,
		UpstreamStream: envelope.Stream,
	}, nil
}

func normalizeCodexRequest(envelope protocol.RequestEnvelope) (NormalizedRequest, error) {
	if envelope.TargetFormat != protocol.FORMAT_OPENAI_RESPONSES {
		return NormalizedRequest{}, unsupportedFormatError("openai-codex-oauth", envelope.TargetFormat)
	}
	request, err := responses.DecodeRequest(envelope.Body)
	if err != nil {
		return NormalizedRequest{}, invalidRequestError("normalize Codex request", err)
	}

	var body map[string]any
	if err := json.Unmarshal(envelope.Body, &body); err != nil {
		return NormalizedRequest{}, invalidRequestError("normalize Codex request", err)
	}
	instructions, input, lifted, err := liftCodexInstructionMessages(request)
	if err != nil {
		return NormalizedRequest{}, invalidRequestError("normalize Codex instructions", err)
	}
	if lifted {
		body["input"] = input
	}
	body["stream"] = true
	body["store"] = false
	body["instructions"] = instructions
	normalizedBody, err := json.Marshal(body)
	if err != nil {
		return NormalizedRequest{}, fmt.Errorf("normalize Codex request: %w", err)
	}
	return NormalizedRequest{
		Body:              normalizedBody,
		UpstreamStream:    true,
		BridgeToNonStream: !envelope.Stream,
	}, nil
}

func liftCodexInstructionMessages(request *responses.Request) (string, []responses.InputItem, bool, error) {
	items, err := responses.DecodeInput(request.Input)
	if err != nil {
		return "", nil, false, err
	}

	instructions := make([]string, 0, 3)
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	input := make([]responses.InputItem, 0, len(items))
	lifted := false
	for index, item := range items {
		if item.Role != "system" && item.Role != "developer" {
			input = append(input, item)
			continue
		}
		if item.Type != "" && item.Type != "message" {
			return "", nil, false, fmt.Errorf("input[%d] instruction role requires a message item", index)
		}
		text, err := codexInstructionText(item.Content)
		if err != nil {
			return "", nil, false, fmt.Errorf("input[%d]: %w", index, err)
		}
		if text != "" {
			instructions = append(instructions, text)
		}
		lifted = true
	}
	return strings.Join(instructions, "\n\n"), input, lifted, nil
}

func codexInstructionText(content responses.ContentList) (string, error) {
	var text strings.Builder
	for index, part := range content {
		switch part.Type {
		case "input_text", "output_text":
			text.WriteString(part.Text)
		default:
			return "", fmt.Errorf("instruction content[%d] type %q is unsupported", index, part.Type)
		}
	}
	return text.String(), nil
}

func normalizeXAIRequest(envelope protocol.RequestEnvelope) (NormalizedRequest, error) {
	switch envelope.TargetFormat {
	case protocol.FORMAT_OPENAI_RESPONSES:
		request, err := responses.DecodeRequest(envelope.Body)
		if err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Responses request", err)
		}
		for _, tool := range request.Tools {
			if tool.Type != "function" {
				return NormalizedRequest{}, unsupportedToolError(tool.Type, envelope.TargetFormat)
			}
		}
	case protocol.FORMAT_OPENAI_CHAT:
		request, err := chat.DecodeRequest(envelope.Body)
		if err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Chat request", err)
		}
		for _, tool := range request.Tools {
			if tool.Type != "function" {
				return NormalizedRequest{}, unsupportedToolError(tool.Type, envelope.TargetFormat)
			}
		}
	default:
		return NormalizedRequest{}, unsupportedFormatError("xai", envelope.TargetFormat)
	}
	return preserveRequest(envelope)
}

func normalizeConcreteProfile(source Profile) (Profile, error) {
	profile := cloneProfile(source)
	profile.ID = strings.ToLower(strings.TrimSpace(profile.ID))
	if profile.ID == "" {
		return Profile{}, fmt.Errorf("profile ID is blank")
	}
	if strings.TrimSpace(profile.Routing.ID) == "" {
		return Profile{}, fmt.Errorf("profile %q routing family is blank", profile.ID)
	}
	profile.CredentialProvider = strings.ToLower(strings.TrimSpace(profile.CredentialProvider))
	if profile.CredentialProvider == "" {
		return Profile{}, fmt.Errorf("profile %q credential provider is blank", profile.ID)
	}
	if !profile.Preferred.Valid() {
		return Profile{}, fmt.Errorf("profile %q preferred format %q is invalid", profile.ID, profile.Preferred)
	}
	if _, ok := profile.Endpoints[profile.Preferred]; !ok {
		return Profile{}, fmt.Errorf("profile %q has no preferred endpoint", profile.ID)
	}
	for format, endpoint := range profile.Endpoints {
		if !format.Valid() || !strings.HasPrefix(endpoint, "/") {
			return Profile{}, fmt.Errorf("profile %q has invalid endpoint %q for %q", profile.ID, endpoint, format)
		}
	}
	if profile.NormalizeRequest == nil {
		return Profile{}, fmt.Errorf("profile %q has nil request normalizer", profile.ID)
	}
	profile.AllowedRequestHeaders = normalizeHeaderNames(profile.AllowedRequestHeaders)
	profile.AllowedResponseHeaders = normalizeHeaderNames(profile.AllowedResponseHeaders)
	return profile, nil
}

func cloneProfile(source Profile) Profile {
	profile := source
	profile.Routing.Qualifiers = slices.Clone(source.Routing.Qualifiers)
	profile.Routing.ExactModels = slices.Clone(source.Routing.ExactModels)
	profile.Routing.Prefixes = slices.Clone(source.Routing.Prefixes)
	profile.Endpoints = make(map[protocol.Format]string, len(source.Endpoints))
	for format, endpoint := range source.Endpoints {
		profile.Endpoints[format] = endpoint
	}
	profile.AllowedRequestHeaders = slices.Clone(source.AllowedRequestHeaders)
	profile.AllowedResponseHeaders = slices.Clone(source.AllowedResponseHeaders)
	profile.AdvertisedModels = slices.Clone(source.AdvertisedModels)
	return profile
}

func normalizeHeaderNames(names []string) []string {
	normalized := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		normalized = append(normalized, name)
	}
	return normalized
}

func headerAllowed(name string, allowlist []string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if sensitiveHeader(name) {
		return false
	}
	for _, allowed := range allowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), name) {
			return true
		}
	}
	return false
}

func sensitiveHeader(name string) bool {
	if strings.HasPrefix(name, "x-forwarded-") {
		return true
	}
	switch name {
	case "authorization", "x-api-key", "cookie", "set-cookie", "host",
		"connection", "proxy-connection", "keep-alive", "te", "trailer",
		"transfer-encoding", "upgrade", "proxy-authenticate", "proxy-authorization":
		return true
	default:
		return false
	}
}

func routingProfilesEqual(left, right route.Profile) bool {
	return strings.EqualFold(left.ID, right.ID) &&
		slices.Equal(left.Qualifiers, right.Qualifiers) &&
		slices.Equal(left.ExactModels, right.ExactModels) &&
		slices.Equal(left.Prefixes, right.Prefixes)
}

func invalidRequestError(operation string, err error) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: operation + ": " + err.Error(),
		Cause:   err,
	}
}

func unsupportedFormatError(profileID string, format protocol.Format) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    "unsupported_format",
		Message: fmt.Sprintf("profile %q does not support format %q", profileID, format),
	}
}

func unsupportedCredentialError(providerFamily string, credentialKind auth.Kind) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_AUTH,
		Status:  http.StatusUnauthorized,
		Code:    "unsupported_credential",
		Message: fmt.Sprintf("provider %q does not support credential kind %q", providerFamily, credentialKind),
	}
}

func unsupportedToolError(toolType string, format protocol.Format) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    "unsupported_tool",
		Message: fmt.Sprintf("xAI does not support tool type %q for format %q", toolType, format),
	}
}
