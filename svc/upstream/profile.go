// Package upstream defines concrete provider profiles and upstream transport metadata.
package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	sdkanthropic "github.com/bizshuk/agentsdk/provider/anthropic"
	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	sdkcodex "github.com/bizshuk/agentsdk/provider/codex"
	sdkgoogle "github.com/bizshuk/agentsdk/provider/google"
	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"
	sdkminimax "github.com/bizshuk/agentsdk/provider/minimax"
	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/anthropic"
	"github.com/bizshuk/proxy/model/chat"
	"github.com/bizshuk/proxy/model/responses"
	"github.com/bizshuk/proxy/svc/route"
)

// Provider wire facts — base URLs, endpoint paths, identity headers, client
// versions — are NOT declared here. They belong to the agentsdk provider
// package for that vendor and are referenced from it (ag.*, anthropic.*,
// codex.*, grok.*, ...). A constant only earns a place in this file when it
// is something the proxy itself decides.
const (
	// Catalog keys. These name rows in DefaultCatalog, not anything on the
	// wire, so they stay proxy-owned even where the string coincides with a
	// vendor name.
	XAI_GROK_OAUTH_PROFILE_ID = "xai-grok-oauth"
	ANTIGRAVITY_PROFILE_ID    = "antigravity"

	// CLIENT_IDENTIFIER is how this proxy names itself to gateways that
	// attribute traffic by client. It is deliberately not the reference
	// CLI's name: impersonating it would misreport who is calling.
	CLIENT_IDENTIFIER = "proxy"

	// OPENAI_IMAGE_DEFAULT_MODEL is the proxy's default for OpenAI image
	// generation. OpenAI's public API has no agentsdk adapter (codex covers
	// only the OAuth Responses surface), so this choice has no upstream
	// owner to defer to.
	OPENAI_IMAGE_DEFAULT_MODEL = "gpt-image-2"
)

// OpenAI public-API surface. agentsdk models the Codex OAuth endpoint but
// not api.openai.com, so these paths have no provider package to live in
// yet. Move them out the day an `openai` adapter lands.
const (
	OPENAI_API_BASE_URL        = "https://api.openai.com"
	OPENAI_PATH_RESPONSES      = "/v1/responses"
	OPENAI_PATH_CHAT           = "/v1/chat/completions"
	OPENAI_PATH_IMAGE_GENERATE = "/v1/images/generations"
	OPENAI_PATH_IMAGE_EDIT     = "/v1/images/edits"

	// OPENAI_COMPAT_PATH_CHAT is the Chat Completions path as served by
	// OpenAI-compatible surfaces that carry their own version prefix in the
	// base URL (Google AI Studio's /v1beta/openai root, for example).
	OPENAI_COMPAT_PATH_CHAT = "/chat/completions"

	// RESPONSES_ENCRYPTED_REASONING is an `include` value of the Responses
	// protocol, which this proxy owns in model/responses — not a vendor
	// extension.
	RESPONSES_ENCRYPTED_REASONING = "reasoning.encrypted_content"
)

var codexResponsesLiteModels = map[string]struct{}{
	"gpt-5.6":     {},
	"gpt-5.6-sol": {},
}

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
type NormalizeRequest func(model.RequestEnvelope) (NormalizedRequest, error)

// ApplyCredentialBody fills in request fields that only the credential can
// supply, for providers whose wire format carries account state in the body
// rather than in a header. It is the credential-aware sibling of
// NormalizeRequest, which runs before the credential is resolved and therefore
// cannot see it. Profiles that need nothing leave it nil.
type ApplyCredentialBody func(context.Context, *authmodel.Credential, []byte) ([]byte, error)

// Profile describes one concrete upstream API surface.
type Profile struct {
	ID                 string
	Routing            route.Profile
	CredentialProvider string
	BaseURL            string
	Endpoints          map[model.Format]string
	// StreamEndpoints overrides Endpoints when the caller wants SSE. Only
	// providers that split generation across two paths (Gemini's
	// generateContent vs streamGenerateContent) need it; everyone else streams
	// from the same endpoint and leaves this nil.
	StreamEndpoints         map[model.Format]string
	ImageGenerationBaseURL  string
	ImageGenerationEndpoint string
	ImageEditBaseURL        string
	ImageEditEndpoint       string
	Preferred               model.Format
	AuthScheme              AuthScheme
	// OAuthScheme overrides AuthScheme when the resolved credential is an
	// OAuth one. Anthropic is the reason it exists: the same endpoint reads
	// an API key from x-api-key but an access token from Authorization.
	// Empty means the credential kind makes no difference.
	OAuthScheme                    AuthScheme
	AllowedRequestHeaders          []string
	AllowedResponseHeaders         []string
	AdvertisedModels               []string
	CountTokensEndpoint            string
	AllowsMissingStreamContentType bool
	NormalizeRequest               NormalizeRequest
	ApplyCredentialBody            ApplyCredentialBody
	// ApplyIdentityHeaders stamps the client-identity headers this gateway
	// gates on beyond the credential. See identity.go — the transport calls
	// it blindly so it never has to know which provider it is talking to.
	ApplyIdentityHeaders ApplyIdentityHeaders
}

// ResolveEndpoint returns the fixed endpoint for a supported format.
func (p Profile) ResolveEndpoint(format model.Format) (string, error) {
	endpoint, ok := p.Endpoints[format]
	if !ok {
		return "", unsupportedFormatError(p.ID, format)
	}
	return endpoint, nil
}

// ResolveGenerationEndpoint returns the endpoint for one generation call,
// honoring a provider's separate streaming path when the caller wants SSE.
func (p Profile) ResolveGenerationEndpoint(format model.Format, stream bool) (string, error) {
	if stream {
		if endpoint, ok := p.StreamEndpoints[format]; ok {
			return endpoint, nil
		}
	}
	return p.ResolveEndpoint(format)
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
func (c *Catalog) ResolveProfile(providerFamily string, credentialKind authmodel.Kind, forcedTarget *model.Format) (Profile, model.Format, error) {
	family := strings.ToLower(strings.TrimSpace(providerFamily))
	profileID := family
	switch family {
	case "openai":
		switch credentialKind {
		case authmodel.KIND_API_KEY:
			profileID = "openai-api"
		case authmodel.KIND_OAUTH:
			profileID = "openai-codex-oauth"
		default:
			return Profile{}, "", unsupportedCredentialError(family, credentialKind)
		}
	case "xai":
		switch credentialKind {
		case authmodel.KIND_API_KEY:
			profileID = "xai"
		case authmodel.KIND_OAUTH:
			profileID = XAI_GROK_OAUTH_PROFILE_ID
		default:
			return Profile{}, "", unsupportedCredentialError(family, credentialKind)
		}
	}
	profile, ok := c.Lookup(profileID)
	if !ok {
		return Profile{}, "", &model.ProxyError{
			Kind:    model.ERROR_UNKNOWN_MODEL,
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
	xaiRouting := route.Profile{
		ID:         "xai",
		Qualifiers: []string{"xai", "xai-responses", "xai-chat", "xai-messages"},
		Prefixes:   []string{"grok-"},
	}
	profiles := []Profile{
		{
			ID:                     "anthropic",
			Routing:                route.Profile{ID: "anthropic", Qualifiers: []string{"anthropic"}, Prefixes: []string{"claude-"}},
			CredentialProvider:     "anthropic",
			BaseURL:                sdkanthropic.DefaultBaseURL,
			Endpoints:              map[model.Format]string{model.FORMAT_ANTHROPIC_MESSAGES: sdkanthropic.PATH_MESSAGES},
			Preferred:              model.FORMAT_ANTHROPIC_MESSAGES,
			AuthScheme:             AUTH_X_API_KEY,
			OAuthScheme:            AUTH_BEARER,
			AllowedRequestHeaders:  append(slices.Clone(defaultRequestHeaders), sdkanthropic.OAuthBetaHeader),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"claude-"},
			CountTokensEndpoint:    sdkanthropic.PATH_COUNT_TOKENS,
			NormalizeRequest:       preserveRequest,
			ApplyIdentityHeaders:   applyAnthropicIdentity,
		},
		{
			ID:                 "minimax",
			Routing:            route.Profile{ID: "minimax", Qualifiers: []string{"minimax"}, ExactModels: []string{"MiniMax-Text-01"}, Prefixes: []string{"minimax-"}},
			CredentialProvider: "minimax",
			BaseURL:            sdkminimax.DefaultBaseURL,
			// MiniMax fronts an Anthropic-Messages-compatible surface, so the
			// path is Anthropic's, not one of its own.
			Endpoints:              map[model.Format]string{model.FORMAT_ANTHROPIC_MESSAGES: sdkanthropic.PATH_MESSAGES},
			Preferred:              model.FORMAT_ANTHROPIC_MESSAGES,
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
			BaseURL:            OPENAI_API_BASE_URL,
			Endpoints: map[model.Format]string{
				model.FORMAT_OPENAI_RESPONSES: OPENAI_PATH_RESPONSES,
				model.FORMAT_OPENAI_CHAT:      OPENAI_PATH_CHAT,
			},
			Preferred:               model.FORMAT_OPENAI_RESPONSES,
			AuthScheme:              AUTH_BEARER,
			AllowedRequestHeaders:   append(slices.Clone(defaultRequestHeaders), OPENAI_SAFETY_IDENTIFIER_HEADER),
			AllowedResponseHeaders:  slices.Clone(defaultResponseHeaders),
			ImageGenerationBaseURL:  OPENAI_API_BASE_URL,
			ImageGenerationEndpoint: OPENAI_PATH_IMAGE_GENERATE,
			ImageEditBaseURL:        OPENAI_API_BASE_URL,
			ImageEditEndpoint:       OPENAI_PATH_IMAGE_EDIT,
			AdvertisedModels:        []string{"gpt-", "o1-", "o3-"},
			NormalizeRequest:        preserveRequest,
		},
		{
			ID:                             "openai-codex-oauth",
			Routing:                        route.Profile{ID: "openai", Qualifiers: []string{"openai", "openai-chat"}, Prefixes: []string{"gpt-", "o1-", "o3-"}},
			CredentialProvider:             "openai",
			BaseURL:                        sdkcodex.DefaultBaseURL,
			Endpoints:                      map[model.Format]string{model.FORMAT_OPENAI_RESPONSES: sdkcodex.PATH_RESPONSES},
			Preferred:                      model.FORMAT_OPENAI_RESPONSES,
			AuthScheme:                     AUTH_BEARER,
			AllowedRequestHeaders:          slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders:         slices.Clone(defaultResponseHeaders),
			AdvertisedModels:               []string{"gpt-", "o1-", "o3-"},
			AllowsMissingStreamContentType: true,
			NormalizeRequest:               normalizeCodexRequest,
			ApplyIdentityHeaders:           applyCodexIdentity,
		},
		{
			ID:                      "xai",
			Routing:                 xaiRouting,
			CredentialProvider:      "xai",
			BaseURL:                 sdkgrok.APIBaseURL,
			ImageGenerationBaseURL:  sdkgrok.IMAGE_BASE_URL,
			ImageGenerationEndpoint: sdkgrok.IMAGE_PATH,
			// xAI serves OpenAI-compatible dialects at OpenAI's own paths.
			Endpoints: map[model.Format]string{
				model.FORMAT_OPENAI_RESPONSES: OPENAI_PATH_RESPONSES,
				model.FORMAT_OPENAI_CHAT:      OPENAI_PATH_CHAT,
			},
			Preferred:              model.FORMAT_OPENAI_RESPONSES,
			AuthScheme:             AUTH_BEARER,
			AllowedRequestHeaders:  slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"grok-"},
			NormalizeRequest:       normalizeXAIRequest,
			ApplyIdentityHeaders:   applyXAIIdentity,
		},
		{
			ID:                      XAI_GROK_OAUTH_PROFILE_ID,
			Routing:                 xaiRouting,
			CredentialProvider:      "xai",
			BaseURL:                 sdkgrok.OAuthBaseURL,
			ImageGenerationBaseURL:  sdkgrok.IMAGE_BASE_URL,
			ImageGenerationEndpoint: sdkgrok.IMAGE_PATH,
			Endpoints: map[model.Format]string{
				model.FORMAT_OPENAI_RESPONSES:   sdkgrok.OAUTH_PATH_RESPONSES,
				model.FORMAT_OPENAI_CHAT:        sdkgrok.OAUTH_PATH_CHAT,
				model.FORMAT_ANTHROPIC_MESSAGES: sdkgrok.OAUTH_PATH_MESSAGES,
			},
			Preferred:  model.FORMAT_OPENAI_RESPONSES,
			AuthScheme: AUTH_BEARER,
			AllowedRequestHeaders: append(slices.Clone(defaultRequestHeaders),
				sdkgrok.ConversationIDHeader,
				sdkgrok.RequestIDHeader,
				sdkgrok.SessionIDHeader,
				sdkgrok.TurnIndexHeader,
				sdkgrok.AgentIDHeader,
				sdkgrok.DeploymentIDHeader,
			),
			AllowedResponseHeaders: append(slices.Clone(defaultResponseHeaders),
				sdkgrok.ContextWindowHeader,
				sdkgrok.MaxCompletionTokensHeader,
				sdkgrok.ModelsETagHeader,
				sdkgrok.ShouldRetryHeader,
			),
			AdvertisedModels:     []string{"grok-"},
			NormalizeRequest:     normalizeXAIGrokOAuthRequest,
			ApplyIdentityHeaders: applyXAIGrokOAuthIdentity,
		},
		{
			// Antigravity speaks Gemini generateContent wrapped in a routing
			// envelope, so it is its own provider format rather than a variant
			// of the public Gemini profile below.
			ID: ANTIGRAVITY_PROFILE_ID,
			Routing: route.Profile{
				ID:         ANTIGRAVITY_PROFILE_ID,
				Qualifiers: []string{ANTIGRAVITY_PROFILE_ID},
				// Only IDs the public Gemini/Anthropic APIs never serve are
				// claimed by bare name; everything else needs the
				// `antigravity/` qualifier so this profile cannot hijack a
				// model another provider owns.
				ExactModels: []string{
					"gemini-3.6-flash-high", "gemini-3.6-flash-medium", "gemini-3.6-flash-low",
					"gemini-3.5-flash-high", "gemini-3.5-flash-medium", "gemini-3.5-flash-low",
					"gemini-3.1-pro-high", "gemini-3.1-pro-low",
					"gpt-oss-120b-medium",
				},
			},
			CredentialProvider:     ANTIGRAVITY_PROFILE_ID,
			BaseURL:                ag.FallbackBaseURL,
			Endpoints:              map[model.Format]string{model.FORMAT_ANTIGRAVITY: ag.PATH_GENERATE},
			StreamEndpoints:        map[model.Format]string{model.FORMAT_ANTIGRAVITY: ag.PATH_STREAM},
			Preferred:              model.FORMAT_ANTIGRAVITY,
			AuthScheme:             AUTH_BEARER,
			AllowedRequestHeaders:  slices.Clone(defaultRequestHeaders),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			// Only bare-name-routable IDs are advertised. Antigravity's
			// claude-* models are reachable as `antigravity/claude-sonnet-4-6`
			// but are deliberately not listed: a bare claude- name routes to
			// Anthropic, so advertising them here would hand callers a model
			// string that lands somewhere else.
			AdvertisedModels: []string{
				"gemini-3.6-flash-high", "gemini-3.6-flash-medium", "gemini-3.6-flash-low",
				"gemini-3.5-flash-high", "gemini-3.5-flash-medium", "gemini-3.5-flash-low",
				"gemini-3.1-pro-high", "gemini-3.1-pro-low",
				"gpt-oss-120b-medium",
			},
			NormalizeRequest:     preserveRequest,
			ApplyCredentialBody:  applyAntigravityCredentialBody,
			ApplyIdentityHeaders: applyAntigravityIdentity,
		},
		{
			ID:                 "google",
			Routing:            route.Profile{ID: "google", Qualifiers: []string{"google", "google-chat"}, Prefixes: []string{"gemini-", "gemma-", "imagen-"}},
			CredentialProvider: "google",
			BaseURL:            sdkgoogle.DefaultBaseURL,
			Endpoints:          map[model.Format]string{model.FORMAT_OPENAI_CHAT: OPENAI_COMPAT_PATH_CHAT},
			Preferred:          model.FORMAT_OPENAI_CHAT,
			AuthScheme:         AUTH_BEARER,
			AllowedRequestHeaders: append(slices.Clone(defaultRequestHeaders),
				"x-goog-api-key"),
			AllowedResponseHeaders: slices.Clone(defaultResponseHeaders),
			AdvertisedModels:       []string{"gemini-", "gemma-", "imagen-"},
			NormalizeRequest:       preserveRequest,
		},
	}
	return NewCatalog(profiles)
}

func preserveRequest(envelope model.RequestEnvelope) (NormalizedRequest, error) {
	return NormalizedRequest{
		Body:           envelope.Body,
		UpstreamStream: envelope.Stream,
	}, nil
}

func normalizeCodexRequest(envelope model.RequestEnvelope) (NormalizedRequest, error) {
	if envelope.TargetFormat != model.FORMAT_OPENAI_RESPONSES {
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
	// The Codex /codex/responses endpoint rejects max_output_tokens; strip it before
	// forwarding so Anthropic-side max_tokens can flow through other providers safely.
	delete(body, "max_output_tokens")
	body["stream"] = true
	body["store"] = false
	body["instructions"] = instructions
	if isCodexResponsesLiteModel(request.Model) {
		body["parallel_tool_calls"] = false
	}
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

func isCodexResponsesLiteModel(modelName string) bool {
	_, ok := codexResponsesLiteModels[strings.ToLower(strings.TrimSpace(modelName))]
	return ok
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

func normalizeXAIRequest(envelope model.RequestEnvelope) (NormalizedRequest, error) {
	switch envelope.TargetFormat {
	case model.FORMAT_OPENAI_RESPONSES:
		request, err := responses.DecodeRequest(envelope.Body)
		if err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Responses request", err)
		}
		for _, tool := range request.Tools {
			if tool.Type != "function" {
				return NormalizedRequest{}, unsupportedToolError(tool.Type, envelope.TargetFormat)
			}
		}
	case model.FORMAT_OPENAI_CHAT:
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

func normalizeXAIGrokOAuthRequest(envelope model.RequestEnvelope) (NormalizedRequest, error) {
	var body map[string]any
	if err := json.Unmarshal(envelope.Body, &body); err != nil {
		return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth request", err)
	}
	if body == nil {
		return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth request", fmt.Errorf("JSON object is required"))
	}

	switch envelope.TargetFormat {
	case model.FORMAT_OPENAI_RESPONSES:
		if _, err := responses.DecodeRequest(envelope.Body); err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth Responses request", err)
		}
		if value, exists := body["store"]; !exists || value == nil {
			body["store"] = false
		}
		if err := ensureJSONArrayContainsString(body, "include", RESPONSES_ENCRYPTED_REASONING); err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth Responses request", err)
		}
		if envelope.Stream {
			body["stream"] = true
		}
	case model.FORMAT_OPENAI_CHAT:
		if _, err := chat.DecodeRequest(envelope.Body); err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth Chat request", err)
		}
		if envelope.Stream {
			body["stream"] = true
			options, err := objectField(body, "stream_options")
			if err != nil {
				return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth Chat request", err)
			}
			options["include_usage"] = true
			body["stream_options"] = options
		}
	case model.FORMAT_ANTHROPIC_MESSAGES:
		request, err := anthropic.DecodeRequest(envelope.Body)
		if err != nil {
			return NormalizedRequest{}, invalidRequestError("normalize xAI Grok OAuth Messages request", err)
		}
		if request.MaxTokens == 0 {
			body["max_tokens"] = sdkgrok.DefaultMaxTokens
		}
		if envelope.Stream {
			body["stream"] = true
		}
	default:
		return NormalizedRequest{}, unsupportedFormatError(XAI_GROK_OAUTH_PROFILE_ID, envelope.TargetFormat)
	}

	normalizedBody, err := json.Marshal(body)
	if err != nil {
		return NormalizedRequest{}, fmt.Errorf("normalize xAI Grok OAuth request: %w", err)
	}
	return NormalizedRequest{
		Body:           normalizedBody,
		UpstreamStream: envelope.Stream,
	}, nil
}

func ensureJSONArrayContainsString(body map[string]any, field, required string) error {
	raw, exists := body[field]
	if !exists || raw == nil {
		body[field] = []any{required}
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("%s must be an array", field)
	}
	for _, value := range values {
		item, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must contain only strings", field)
		}
		if item == required {
			return nil
		}
	}
	body[field] = append(values, required)
	return nil
}

func objectField(body map[string]any, field string) (map[string]any, error) {
	raw, exists := body[field]
	if !exists || raw == nil {
		return make(map[string]any), nil
	}
	value, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	return value, nil
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
	profile.Endpoints = make(map[model.Format]string, len(source.Endpoints))
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
	return &model.ProxyError{
		Kind:    model.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: operation + ": " + err.Error(),
		Cause:   err,
	}
}

func unsupportedFormatError(profileID string, format model.Format) error {
	return &model.ProxyError{
		Kind:    model.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    "unsupported_format",
		Message: fmt.Sprintf("profile %q does not support format %q", profileID, format),
	}
}

func unsupportedCredentialError(providerFamily string, credentialKind authmodel.Kind) error {
	return &model.ProxyError{
		Kind:    model.ERROR_AUTH,
		Status:  http.StatusUnauthorized,
		Code:    "unsupported_credential",
		Message: fmt.Sprintf("provider %q does not support credential kind %q", providerFamily, credentialKind),
	}
}

func unsupportedToolError(toolType string, format model.Format) error {
	return &model.ProxyError{
		Kind:    model.ERROR_UNSUPPORTED_FEATURE,
		Status:  http.StatusBadRequest,
		Code:    "unsupported_tool",
		Message: fmt.Sprintf("xAI does not support tool type %q for format %q", toolType, format),
	}
}
