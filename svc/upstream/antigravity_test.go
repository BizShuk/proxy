package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestInjectAntigravityProjectStampsEnvelope(t *testing.T) {
	body, err := injectAntigravityProject(
		[]byte(`{"model":"gemini-3.1-pro-high","request":{"contents":[]}}`), "projects/demo")
	require.NoError(t, err)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(body, &envelope))
	assert.Equal(t, "projects/demo", envelope["project"])
	assert.Equal(t, "gemini-3.1-pro-high", envelope["model"])
	assert.Contains(t, envelope, "request")
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

func TestAntigravityProjectResolverPrefersCredentialProject(t *testing.T) {
	resolver := newAntigravityProjects()

	project, err := resolver.Resolve(context.Background(), &authmodel.Credential{
		Provider: ANTIGRAVITY_PROFILE_ID, Kind: authmodel.KIND_OAUTH,
		AccessToken: "at", ProjectID: "projects/stored",
	})
	require.NoError(t, err)
	assert.Equal(t, "projects/stored", project)
}

// The project is issued by loadCodeAssist, not by the OAuth grant, so
// credentials minted elsewhere must be resolvable without a re-login — and the
// lookup must happen once per credential, not once per request.
func TestAntigravityProjectResolverFetchesAndCaches(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "/v1internal:loadCodeAssist", r.URL.Path)
		assert.Equal(t, "Bearer at", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cloudaicompanionProject":"projects/fetched"}`))
	}))
	defer server.Close()

	resolver := newAntigravityProjects()
	resolver.baseURL = server.URL
	cred := &authmodel.Credential{
		Provider: ANTIGRAVITY_PROFILE_ID, Kind: authmodel.KIND_OAUTH,
		Account: "dev@example.com", AccessToken: "at",
	}

	first, err := resolver.Resolve(context.Background(), cred)
	require.NoError(t, err)
	assert.Equal(t, "projects/fetched", first)

	second, err := resolver.Resolve(context.Background(), cred)
	require.NoError(t, err)
	assert.Equal(t, "projects/fetched", second)
	assert.Equal(t, 1, calls)
}

// A brand-new account has no provisioned project. agentsdk treats that as the
// normal first-run state and falls back to the sentinel project the reference
// clients use, so the request proceeds instead of failing.
func TestAntigravityProjectResolverFallsBackToSentinelProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"allowedTiers":[{"id":"free-tier"}]}`))
	}))
	defer server.Close()

	resolver := newAntigravityProjects()
	resolver.baseURL = server.URL

	project, err := resolver.Resolve(context.Background(), &authmodel.Credential{
		Provider: ANTIGRAVITY_PROFILE_ID, Kind: authmodel.KIND_OAUTH,
		Account: "new@example.com", AccessToken: "at",
	})
	require.NoError(t, err)
	assert.Equal(t, ag.DefaultProjectID, project)
}
