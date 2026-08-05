package upstream

import (
	"net/http"
	"testing"

	sdkanthropic "github.com/bizshuk/agentsdk/provider/anthropic"
	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	sdkcodex "github.com/bizshuk/agentsdk/provider/codex"
	sdkgoogle "github.com/bizshuk/agentsdk/provider/google"
	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"
	sdkminimax "github.com/bizshuk/agentsdk/provider/minimax"
	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the responsibility boundary rather than any single value:
// a vendor's base URL, endpoint path and identity headers must come from its
// agentsdk provider package, so re-declaring one in this package fails here.
// Only api.openai.com is exempt, because agentsdk has no adapter for it.

func TestProfileWireFactsComeFromAgentSDK(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	cases := []struct {
		profileID string
		baseURL   string
		endpoints map[model.Format]string
	}{
		{"anthropic", sdkanthropic.DefaultBaseURL, map[model.Format]string{
			model.FORMAT_ANTHROPIC_MESSAGES: sdkanthropic.PATH_MESSAGES,
		}},
		{"minimax", sdkminimax.DefaultBaseURL, map[model.Format]string{
			model.FORMAT_ANTHROPIC_MESSAGES: sdkanthropic.PATH_MESSAGES,
		}},
		{"openai-codex-oauth", sdkcodex.DefaultBaseURL, map[model.Format]string{
			model.FORMAT_OPENAI_RESPONSES: sdkcodex.PATH_RESPONSES,
		}},
		{"xai", sdkgrok.APIBaseURL, nil},
		{XAI_GROK_OAUTH_PROFILE_ID, sdkgrok.OAuthBaseURL, map[model.Format]string{
			model.FORMAT_OPENAI_RESPONSES:   sdkgrok.OAUTH_PATH_RESPONSES,
			model.FORMAT_OPENAI_CHAT:        sdkgrok.OAUTH_PATH_CHAT,
			model.FORMAT_ANTHROPIC_MESSAGES: sdkgrok.OAUTH_PATH_MESSAGES,
		}},
		{ANTIGRAVITY_PROFILE_ID, ag.FallbackBaseURL, map[model.Format]string{
			model.FORMAT_ANTIGRAVITY: ag.PATH_GENERATE,
		}},
		{"google", sdkgoogle.DefaultBaseURL, nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.profileID, func(t *testing.T) {
			profile, ok := catalog.Lookup(testCase.profileID)
			require.True(t, ok)
			assert.Equal(t, testCase.baseURL, profile.BaseURL)
			for format, endpoint := range testCase.endpoints {
				assert.Equal(t, endpoint, profile.Endpoints[format])
			}
		})
	}

	anthropicProfile, ok := catalog.Lookup("anthropic")
	require.True(t, ok)
	assert.Equal(t, sdkanthropic.PATH_COUNT_TOKENS, anthropicProfile.CountTokensEndpoint)

	xaiProfile, ok := catalog.Lookup("xai")
	require.True(t, ok)
	assert.Equal(t, sdkgrok.IMAGE_BASE_URL, xaiProfile.ImageGenerationBaseURL)
	assert.Equal(t, sdkgrok.IMAGE_PATH, xaiProfile.ImageGenerationEndpoint)
}

// The transport must not branch on provider identity: every gateway that
// needs more than the credential declares a hook, and client.go calls it
// blindly. A profile losing its hook silently drops identity headers, so
// the presence of one is asserted here rather than inferred from behavior.
func TestProfilesDeclareIdentityHooks(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	for _, profileID := range []string{
		"anthropic", "openai-codex-oauth", "xai", XAI_GROK_OAUTH_PROFILE_ID, ANTIGRAVITY_PROFILE_ID,
	} {
		profile, ok := catalog.Lookup(profileID)
		require.True(t, ok, profileID)
		assert.NotNil(t, profile.ApplyIdentityHeaders, profileID)
	}
}

// The two xAI credential flavors must never share an inference host: an
// OAuth token is minted for cli-chat-proxy and the public host was never
// issued it.
func TestXAICredentialFlavorsUseDistinctHosts(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)

	apiKeyProfile, ok := catalog.Lookup("xai")
	require.True(t, ok)
	oauthProfile, ok := catalog.Lookup(XAI_GROK_OAUTH_PROFILE_ID)
	require.True(t, ok)

	assert.NotEqual(t, apiKeyProfile.BaseURL, oauthProfile.BaseURL)
	assert.Equal(t, apiKeyProfile.Routing.ID, oauthProfile.Routing.ID)

	// Imagine is the one surface both flavors share, by design.
	assert.Equal(t, apiKeyProfile.ImageGenerationBaseURL, oauthProfile.ImageGenerationBaseURL)
}

// Anthropic reads an API key from x-api-key but an access token from
// Authorization. That is data on the Profile now, not a branch in the
// transport, so both directions are pinned.
func TestAnthropicCredentialSchemeSwitchesOnKind(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	profile, ok := catalog.Lookup("anthropic")
	require.True(t, ok)

	apiKeyHeader := http.Header{}
	applyCredentialHeader(profile, &authmodel.Credential{Kind: authmodel.KIND_API_KEY, APIKey: "sk-test"}, apiKeyHeader)
	assert.Equal(t, "sk-test", apiKeyHeader.Get("x-api-key"))
	assert.Empty(t, apiKeyHeader.Get("Authorization"))

	oauthHeader := http.Header{}
	applyCredentialHeader(profile, &authmodel.Credential{Kind: authmodel.KIND_OAUTH, AccessToken: "tok-test"}, oauthHeader)
	assert.Equal(t, "Bearer tok-test", oauthHeader.Get("Authorization"))
	assert.Empty(t, oauthHeader.Get("x-api-key"))
}
