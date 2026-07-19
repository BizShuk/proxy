package upstream

import (
	"encoding/json"
	"net/http"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/model/responses"
	"github.com/bizshuk/proxy/svc/route"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCatalogCapabilities(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	tests := []struct {
		id                             string
		preferred                      model.Format
		baseURL                        string
		endpoints                      map[model.Format]string
		allowsMissingStreamContentType bool
	}{
		{
			id: "anthropic", preferred: model.FORMAT_ANTHROPIC_MESSAGES,
			baseURL:   "https://api.anthropic.com",
			endpoints: map[model.Format]string{model.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"},
		},
		{
			id: "minimax", preferred: model.FORMAT_ANTHROPIC_MESSAGES,
			baseURL:   "https://api.minimax.io/anthropic",
			endpoints: map[model.Format]string{model.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"},
		},
		{
			id: "openai-api", preferred: model.FORMAT_OPENAI_RESPONSES,
			baseURL: "https://api.openai.com",
			endpoints: map[model.Format]string{
				model.FORMAT_OPENAI_RESPONSES: "/v1/responses",
				model.FORMAT_OPENAI_CHAT:      "/v1/chat/completions",
			},
		},
		{
			id: "openai-codex-oauth", preferred: model.FORMAT_OPENAI_RESPONSES,
			baseURL:                        "https://chatgpt.com/backend-api",
			endpoints:                      map[model.Format]string{model.FORMAT_OPENAI_RESPONSES: "/codex/responses"},
			allowsMissingStreamContentType: true,
		},
		{
			id: "xai", preferred: model.FORMAT_OPENAI_RESPONSES,
			baseURL: "https://api.x.ai",
			endpoints: map[model.Format]string{
				model.FORMAT_OPENAI_RESPONSES: "/v1/responses",
				model.FORMAT_OPENAI_CHAT:      "/v1/chat/completions",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			profile, ok := catalog.Lookup(tc.id)
			require.True(t, ok)
			assert.Equal(t, tc.id, profile.ID)
			assert.Equal(t, tc.preferred, profile.Preferred)
			assert.Equal(t, tc.baseURL, profile.BaseURL)
			assert.Equal(t, tc.endpoints, profile.Endpoints)
			assert.Equal(t, tc.allowsMissingStreamContentType, profile.AllowsMissingStreamContentType)
		})
	}
}

func TestCatalogResolveProfile(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	tests := []struct {
		name          string
		family        string
		kind          authmodel.Kind
		forced        *model.Format
		wantID        string
		wantTarget    model.Format
		wantErrorKind model.ErrorKind
	}{
		{name: "OpenAI API defaults Responses", family: "openai", kind: authmodel.KIND_API_KEY, wantID: "openai-api", wantTarget: model.FORMAT_OPENAI_RESPONSES},
		{name: "OpenAI API forced Chat", family: "OPENAI", kind: authmodel.KIND_API_KEY, forced: profileFormatPtr(model.FORMAT_OPENAI_CHAT), wantID: "openai-api", wantTarget: model.FORMAT_OPENAI_CHAT},
		{name: "OpenAI OAuth uses Codex", family: "openai", kind: authmodel.KIND_OAUTH, wantID: "openai-codex-oauth", wantTarget: model.FORMAT_OPENAI_RESPONSES},
		{name: "OpenAI OAuth rejects Chat", family: "openai", kind: authmodel.KIND_OAUTH, forced: profileFormatPtr(model.FORMAT_OPENAI_CHAT), wantErrorKind: model.ERROR_UNSUPPORTED_FEATURE},
		{name: "xAI defaults Responses", family: "xai", kind: authmodel.KIND_API_KEY, wantID: "xai", wantTarget: model.FORMAT_OPENAI_RESPONSES},
		{name: "xAI forced Chat", family: "xai", kind: authmodel.KIND_API_KEY, forced: profileFormatPtr(model.FORMAT_OPENAI_CHAT), wantID: "xai", wantTarget: model.FORMAT_OPENAI_CHAT},
		{name: "unknown family", family: "unknown", kind: authmodel.KIND_API_KEY, wantErrorKind: model.ERROR_UNKNOWN_MODEL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			profile, target, err := catalog.ResolveProfile(tc.family, tc.kind, tc.forced)
			if tc.wantErrorKind != "" {
				var proxyErr *model.ProxyError
				require.ErrorAs(t, err, &proxyErr)
				assert.Equal(t, tc.wantErrorKind, proxyErr.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantID, profile.ID)
			assert.Equal(t, tc.wantTarget, target)
		})
	}
}

func TestCodexNormalizerMarksNonStreamBridge(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("openai-codex-oauth")
	require.True(t, ok)

	tests := []struct {
		name       string
		stream     bool
		wantBridge bool
	}{
		{name: "non-stream", stream: false, wantBridge: true},
		{name: "stream", stream: true, wantBridge: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5","input":"hi","stream":` + assertBool(tc.stream) + `}`)
			normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
				TargetFormat: model.FORMAT_OPENAI_RESPONSES,
				Stream:       tc.stream,
				Body:         body,
			})
			require.NoError(t, err)
			assert.True(t, normalized.UpstreamStream)
			assert.Equal(t, tc.wantBridge, normalized.BridgeToNonStream)

			var got map[string]any
			require.NoError(t, json.Unmarshal(normalized.Body, &got))
			assert.Equal(t, true, got["stream"])
			assert.Equal(t, false, got["store"])
			assert.Equal(t, "", got["instructions"])
		})
	}
}

func TestCodexNormalizerLiftsInstructionMessages(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("openai-codex-oauth")
	require.True(t, ok)

	body := []byte(`{
		"model":"gpt-5.5",
		"instructions":"top-level policy",
		"input":[
			{"type":"message","role":"system","content":[{"type":"input_text","text":"system policy"}]},
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer policy"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}
		],
		"stream":true
	}`)
	normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
		TargetFormat: model.FORMAT_OPENAI_RESPONSES,
		Stream:       true,
		Body:         body,
	})
	require.NoError(t, err)

	request, err := responses.DecodeRequest(normalized.Body)
	require.NoError(t, err)
	assert.Equal(t, "top-level policy\n\nsystem policy\n\ndeveloper policy", request.Instructions)
	items, err := responses.DecodeInput(request.Input)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "user", items[0].Role)
	assert.Equal(t, "hello", items[0].Content[0].Text)
}

func TestCodexNormalizerStripsMaxOutputTokens(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("openai-codex-oauth")
	require.True(t, ok)

	body := []byte(`{
		"model":"gpt-5.5",
		"max_output_tokens":512,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"stream":true
	}`)
	normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
		TargetFormat: model.FORMAT_OPENAI_RESPONSES,
		Stream:       true,
		Body:         body,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(normalized.Body, &got))
	_, present := got["max_output_tokens"]
	assert.False(t, present, "Codex normalizer must strip max_output_tokens before forwarding to /codex/responses")

	request, err := responses.DecodeRequest(normalized.Body)
	require.NoError(t, err)
	assert.Nil(t, request.MaxOutputTokens, "Codex normalizer must leave the decoded Responses MaxOutputTokens nil")
}

func TestCodexNormalizerDisablesParallelToolCallsForResponsesLiteModel(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("openai-codex-oauth")
	require.True(t, ok)

	body := []byte(`{
		"model":"gpt-5.6-sol",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"parallel_tool_calls":true,
		"stream":true
	}`)
	normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
		TargetFormat: model.FORMAT_OPENAI_RESPONSES,
		Stream:       true,
		Body:         body,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(normalized.Body, &got))
	assert.Equal(t, false, got["parallel_tool_calls"])
}

func TestCodexNormalizerPreservesParallelToolCallsForClassicModel(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("openai-codex-oauth")
	require.True(t, ok)

	body := []byte(`{
		"model":"gpt-5.5",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"parallel_tool_calls":true,
		"stream":true
	}`)
	normalized, err := profile.NormalizeRequest(model.RequestEnvelope{
		TargetFormat: model.FORMAT_OPENAI_RESPONSES,
		Stream:       true,
		Body:         body,
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(normalized.Body, &got))
	assert.Equal(t, true, got["parallel_tool_calls"])
}

func TestProfileHeaderAllowlistCannotAdmitSensitiveHeaders(t *testing.T) {
	profile := Profile{
		AllowedRequestHeaders: []string{
			"X-Request-ID", "Authorization", "x-api-key", "Cookie", "Host",
			"Connection", "Proxy-Connection", "X-Forwarded-Authorization",
		},
		AllowedResponseHeaders: []string{"Content-Type", "Set-Cookie", "Connection"},
	}

	assert.True(t, profile.AllowsRequestHeader("x-request-id"))
	assert.True(t, profile.AllowsResponseHeader("CONTENT-TYPE"))
	for _, header := range []string{
		"Authorization", "x-api-key", "cookie", "host", "connection",
		"proxy-connection", "keep-alive", "transfer-encoding", "upgrade",
		"x-forwarded-for", "X-Forwarded-Authorization",
	} {
		assert.False(t, profile.AllowsRequestHeader(header), header)
	}
	assert.False(t, profile.AllowsResponseHeader("set-cookie"))
	assert.False(t, profile.AllowsResponseHeader("connection"))
}

func TestXAINormalizerRejectsUnsupportedTools(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("xai")
	require.True(t, ok)

	_, err = profile.NormalizeRequest(model.RequestEnvelope{
		TargetFormat: model.FORMAT_OPENAI_RESPONSES,
		Body:         []byte(`{"model":"grok-4.5","input":"hi","tools":[{"type":"web_search"}]}`),
	})
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}

func TestOrdinaryNormalizerPreservesBody(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("anthropic")
	require.True(t, ok)
	body := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[],"stream":true}`)

	normalized, err := profile.NormalizeRequest(model.RequestEnvelope{Stream: true, Body: body})
	require.NoError(t, err)
	assert.Equal(t, body, normalized.Body)
	assert.True(t, normalized.UpstreamStream)
	assert.False(t, normalized.BridgeToNonStream)
}

func TestDefaultCatalogRoutingProfiles(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	router, err := catalog.NewRouter()
	require.NoError(t, err)
	routed, err := router.Resolve(model.FORMAT_OPENAI_CHAT, "grok-4.5")
	require.NoError(t, err)
	assert.Equal(t, "xai", routed.ProviderID)
}

func TestAnthropicProfileHeaderMetadata(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("anthropic")
	require.True(t, ok)

	assert.Equal(t, "2023-06-01", profile.AnthropicVersion)
	assert.True(t, profile.AllowsRequestHeader("anthropic-beta"))
	assert.False(t, profile.AllowsRequestHeader("anthropic-version"))
	assert.Equal(t, "/v1/messages/count_tokens", profile.CountTokensEndpoint)
	assert.Equal(t, []string{"content-type", "retry-after", "x-request-id", "request-id", "cf-ray"}, profile.AllowedResponseHeaders)
	assert.Equal(t, http.CanonicalHeaderKey("anthropic-version"), http.CanonicalHeaderKey("Anthropic-Version"))
}

func TestCatalogAdvertisedModelsAreDeterministicAndUnique(t *testing.T) {
	catalog, err := NewCatalog([]Profile{
		{
			ID: "b", Routing: route.Profile{ID: "b", Qualifiers: []string{"b"}},
			CredentialProvider: "b", BaseURL: "https://b.example.com",
			Endpoints: map[model.Format]string{model.FORMAT_OPENAI_CHAT: "/v1/chat/completions"},
			Preferred: model.FORMAT_OPENAI_CHAT, NormalizeRequest: preserveRequest,
			AdvertisedModels: []string{"shared", "z-model"},
		},
		{
			ID: "a", Routing: route.Profile{ID: "a", Qualifiers: []string{"a"}},
			CredentialProvider: "a", BaseURL: "https://a.example.com",
			Endpoints: map[model.Format]string{model.FORMAT_OPENAI_CHAT: "/v1/chat/completions"},
			Preferred: model.FORMAT_OPENAI_CHAT, NormalizeRequest: preserveRequest,
			AdvertisedModels: []string{"a-model", "shared"},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"a-model", "shared", "z-model"}, catalog.AdvertisedModels())
}

func profileFormatPtr(value model.Format) *model.Format {
	return &value
}

func assertBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
