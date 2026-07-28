package upstream

import (
	"net/http"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareOpenAIRealtimeTargetReplacesCredentialAndFiltersRequestHeaders(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	source := make(http.Header)
	source.Set("Authorization", "Bearer downstream-proxy-key")
	source.Set("X-Api-Key", "downstream-proxy-key")
	source.Set("Cookie", "downstream=session")
	source.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	source.Set(OPENAI_SAFETY_IDENTIFIER_HEADER, "privacy-preserving-user-hash")

	target, err := PrepareOpenAIRealtimeTarget(
		catalog,
		&authmodel.Credential{
			Provider: "openai",
			Kind:     authmodel.KIND_API_KEY,
			APIKey:   "upstream-openai-key",
		},
		OPENAI_REALTIME_CALLS_ENDPOINT,
		source,
	)
	require.NoError(t, err)

	assert.Equal(t, "https://api.openai.com/v1/realtime/calls", target.URL.String())
	assert.Equal(t, "Bearer upstream-openai-key", target.RequestHeaders.Get("Authorization"))
	assert.Empty(t, target.RequestHeaders.Get("x-api-key"))
	assert.Empty(t, target.RequestHeaders.Get("Cookie"))
	assert.Equal(t, source.Get("Traceparent"), target.RequestHeaders.Get("Traceparent"))
	assert.Equal(t, source.Get(OPENAI_SAFETY_IDENTIFIER_HEADER), target.RequestHeaders.Get(OPENAI_SAFETY_IDENTIFIER_HEADER))
	assert.Equal(t, "Bearer downstream-proxy-key", source.Get("Authorization"))
}

func TestPrepareOpenAIRealtimeTargetRejectsUnsupportedCredentialAndEndpoint(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	tests := []struct {
		name       string
		credential *authmodel.Credential
		endpoint   string
		wantStatus int
		wantCode   string
	}{
		{
			name: "OAuth credential",
			credential: &authmodel.Credential{
				Provider:    "openai",
				Kind:        authmodel.KIND_OAUTH,
				AccessToken: "codex-oauth-token",
			},
			endpoint:   OPENAI_REALTIME_WEBSOCKET_ENDPOINT,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "realtime_api_key_required",
		},
		{
			name: "unknown endpoint",
			credential: &authmodel.Credential{
				Provider: "openai",
				Kind:     authmodel.KIND_API_KEY,
				APIKey:   "upstream-openai-key",
			},
			endpoint:   "/v1/responses",
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_realtime_endpoint",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PrepareOpenAIRealtimeTarget(catalog, tc.credential, tc.endpoint, nil)
			var proxyError *model.ProxyError
			require.ErrorAs(t, err, &proxyError)
			assert.Equal(t, tc.wantStatus, proxyError.StatusCode())
			assert.Equal(t, tc.wantCode, proxyError.Code)
		})
	}
}

func TestRealtimeTargetSanitizesResponseHeaders(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	target, err := PrepareOpenAIRealtimeTarget(
		catalog,
		&authmodel.Credential{
			Provider: "openai",
			Kind:     authmodel.KIND_API_KEY,
			APIKey:   "upstream-openai-key",
		},
		OPENAI_REALTIME_CLIENT_SECRETS_ENDPOINT,
		nil,
	)
	require.NoError(t, err)

	headers := target.SanitizeResponseHeaders(http.Header{
		"Content-Type": []string{"application/json"},
		"X-Request-ID": []string{"upstream-request"},
		"Set-Cookie":   []string{"upstream=session"},
		"Connection":   []string{"keep-alive"},
	})

	assert.Equal(t, "application/json", headers.Get("Content-Type"))
	assert.Equal(t, "upstream-request", headers.Get("X-Request-ID"))
	assert.Empty(t, headers.Get("Set-Cookie"))
	assert.Empty(t, headers.Get("Connection"))
}
