package upstream

import (
	"context"
	"encoding/json"
	"testing"

	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func antigravityProfile(t *testing.T) Profile {
	t.Helper()
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup(ANTIGRAVITY_PROFILE_ID)
	require.True(t, ok)
	return profile
}

// Gemini splits generation across two paths and only emits SSE when alt=sse is
// set, so a streaming call must not reuse the blocking endpoint.
func TestAntigravityProfileSplitsStreamEndpoint(t *testing.T) {
	profile := antigravityProfile(t)

	blocking, err := profile.ResolveGenerationEndpoint(model.FORMAT_ANTIGRAVITY, false)
	require.NoError(t, err)
	assert.Equal(t, ag.PATH_GENERATE, blocking)

	streaming, err := profile.ResolveGenerationEndpoint(model.FORMAT_ANTIGRAVITY, true)
	require.NoError(t, err)
	assert.Equal(t, ag.PATH_STREAM, streaming)
	assert.Contains(t, streaming, "alt=sse")
}

// Profiles without a streaming split must keep using their single endpoint.
func TestResolveGenerationEndpointFallsBackToBlockingPath(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("anthropic")
	require.True(t, ok)

	streaming, err := profile.ResolveGenerationEndpoint(model.FORMAT_ANTHROPIC_MESSAGES, true)
	require.NoError(t, err)
	assert.Equal(t, "/v1/messages", streaming)
}

// The endpoint's fixed query string has to survive URL construction, otherwise
// the gateway answers with chunked JSON instead of SSE.
func TestBuildEndpointURLKeepsFixedQuery(t *testing.T) {
	built, err := buildEndpointURL(ag.FallbackBaseURL, ag.PATH_STREAM)
	require.NoError(t, err)
	assert.Equal(t, ag.FallbackBaseURL+"/v1internal:streamGenerateContent?alt=sse", built)
}

func TestBuildEndpointURLStillRejectsFragments(t *testing.T) {
	_, err := buildEndpointURL(ag.FallbackBaseURL, "/v1internal:generateContent#frag")
	require.Error(t, err)
}

// The Antigravity envelope carries the Cloud Code project in the body, which
// is why Profile declares a credential-aware body hook at all.
func TestAntigravityCredentialBodyStampsProject(t *testing.T) {
	body, err := applyAntigravityCredentialBody(context.Background(),
		&authmodel.Credential{
			Provider: ANTIGRAVITY_PROFILE_ID, Kind: authmodel.KIND_OAUTH,
			AccessToken: "at", ProjectID: "projects/demo",
		},
		[]byte(`{"model":"gemini-3.1-pro-high","request":{"contents":[]}}`))
	require.NoError(t, err)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(body, &envelope))
	assert.Equal(t, "projects/demo", envelope["project"])
	assert.Equal(t, "gemini-3.1-pro-high", envelope["model"])
	assert.Contains(t, envelope, "request")
}

// Profiles that need nothing from the credential must declare no hook, so the
// generic path stays a straight pass-through for them.
func TestOnlyAntigravityDeclaresCredentialBodyHook(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	antigravity, ok := catalog.Lookup(ANTIGRAVITY_PROFILE_ID)
	require.True(t, ok)
	assert.NotNil(t, antigravity.ApplyCredentialBody)

	for _, id := range []string{"anthropic", "openai-api", "google", "xai", "minimax"} {
		profile, ok := catalog.Lookup(id)
		require.Truef(t, ok, "profile %q", id)
		assert.Nilf(t, profile.ApplyCredentialBody, "profile %q must not need one", id)
	}
}

// Antigravity must claim only model IDs no other provider serves; a bare
// claude-/gemini- name still belongs to Anthropic and Google.
func TestAntigravityRoutingDoesNotHijackOtherProviders(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	router, err := catalog.NewRouter()
	require.NoError(t, err)

	qualified, err := router.Resolve(model.FORMAT_ANTHROPIC_MESSAGES, "antigravity/claude-sonnet-4-6")
	require.NoError(t, err)
	assert.Equal(t, ANTIGRAVITY_PROFILE_ID, qualified.ProviderID)
	assert.Equal(t, "claude-sonnet-4-6", qualified.Model)

	exact, err := router.Resolve(model.FORMAT_ANTHROPIC_MESSAGES, "gemini-3.1-pro-high")
	require.NoError(t, err)
	assert.Equal(t, ANTIGRAVITY_PROFILE_ID, exact.ProviderID)

	anthropic, err := router.Resolve(model.FORMAT_ANTHROPIC_MESSAGES, "claude-sonnet-4-5")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", anthropic.ProviderID)

	google, err := router.Resolve(model.FORMAT_ANTHROPIC_MESSAGES, "gemini-2.5-pro")
	require.NoError(t, err)
	assert.Equal(t, "google", google.ProviderID)
}

// 憑證沒帶 project 時必須明確報錯而不是靜靜送出註定被拒的請求。
// 解析 project 是 auth 登入流程的事,proxy 不代為補查。
func TestAntigravityCredentialBodyRequiresProject(t *testing.T) {
	_, err := applyAntigravityCredentialBody(context.Background(),
		&authmodel.Credential{
			Provider: ANTIGRAVITY_PROFILE_ID, Kind: authmodel.KIND_OAUTH, AccessToken: "at",
		},
		[]byte(`{"model":"m","request":{"contents":[]}}`))

	require.Error(t, err)
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, "antigravity_project_missing", proxyErr.Code)
}
