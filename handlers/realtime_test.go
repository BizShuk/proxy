package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	utils "github.com/bizshuk/auth/utils"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRealtimeHandlerValidatesDependencies(t *testing.T) {
	_, err := NewRealtimeHandler(RealtimeHandlerDeps{})
	require.Error(t, err)

	store, err := utils.NewFileStore(t.TempDir())
	require.NoError(t, err)
	resolver := upstream.NewCredentialResolver(store, func(c *authmodel.Credential) (authmodel.Authenticator, error) {
		return nil, nil
	}, nil)

	handler, err := NewRealtimeHandler(RealtimeHandlerDeps{
		Credentials: resolver,
	})
	require.NoError(t, err)
	assert.Equal(t, 32, handler.deps.MaxConnections)
	assert.Equal(t, int64(1<<20), handler.deps.MaxHandshakeBytes)
}

func TestNewServerWiresRealtimeRoutes(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.Realtime.Enabled = true

	server, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, server)

	routes := make(map[string]bool)
	for _, route := range server.engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}

	assert.True(t, routes["GET /v1/realtime"])
	assert.True(t, routes["POST /v1/realtime/calls"])
	assert.True(t, routes["POST /v1/realtime/client_secrets"])
}

func TestRealtimeHandshakeRequiresOpenAICredential(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.Realtime.Enabled = true

	server, err := New(cfg)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", bytes.NewBufferString("{}"))
	req.Header.Set("x-api-key", "proxy-test-key")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.engine.ServeHTTP(resp, req)

	// Since no openai credential is in the test store, it should return credential_unavailable (503)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Code)
	assert.Contains(t, resp.Body.String(), "credential")
}
