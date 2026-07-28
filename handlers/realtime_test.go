package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	utils "github.com/bizshuk/auth/utils"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRealtimeHandlerValidatesDependencies(t *testing.T) {
	valid := realtimeHandlerDeps(t, nil)
	tests := []struct {
		name   string
		mutate func(*RealtimeHandlerDeps)
	}{
		{name: "nil catalog", mutate: func(deps *RealtimeHandlerDeps) { deps.Catalog = nil }},
		{name: "nil credentials", mutate: func(deps *RealtimeHandlerDeps) { deps.Credentials = nil }},
		{name: "zero connections", mutate: func(deps *RealtimeHandlerDeps) { deps.MaxConnections = 0 }},
		{name: "zero handshake limit", mutate: func(deps *RealtimeHandlerDeps) { deps.MaxHandshakeBytes = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := valid
			tc.mutate(&deps)
			handler, err := NewRealtimeHandler(deps)
			require.Error(t, err)
			assert.Nil(t, handler)
		})
	}

	handler, err := NewRealtimeHandler(valid)
	require.NoError(t, err)
	assert.Equal(t, valid.MaxConnections, cap(handler.connectionSlots))
	assert.Equal(t, valid.MaxHandshakeBytes, handler.maxHandshakeBytes)
}

func TestNewServerWiresRealtimeRoutes(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.Realtime.Enabled = true
	cfg.Realtime.MaxConnections = 2
	cfg.Realtime.MaxHandshakeBytes = 1 << 10

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

func TestRealtimeWebSocketValidatesUpgradeAndModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := NewRealtimeHandler(realtimeHandlerDeps(t, nil))
	require.NoError(t, err)
	router := gin.New()
	router.GET("/v1/realtime", handler.HandleWebSocket())

	req := httptest.NewRequest(http.MethodGet, "/v1/realtime?model=gpt-realtime", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusUpgradeRequired, resp.Code)
	assert.Contains(t, resp.Body.String(), "websocket_upgrade_required")

	req = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	assert.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "model_required")
}

func TestRealtimeHandshakeRejectsOversizedBodyBeforeCredentialLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deps := realtimeHandlerDeps(t, nil)
	deps.MaxHandshakeBytes = 4
	handler, err := NewRealtimeHandler(deps)
	require.NoError(t, err)
	router := gin.New()
	router.POST("/v1/realtime/calls", handler.HandleHandshake(upstream.OPENAI_REALTIME_CALLS_ENDPOINT))

	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", bytes.NewBufferString("12345"))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.Code)
	assert.Contains(t, resp.Body.String(), "realtime_handshake_too_large")
}

func TestRealtimeHandshakeReplacesCredentialAndFiltersHeaders(t *testing.T) {
	type capturedRequest struct {
		body   []byte
		header http.Header
		path   string
	}
	captured := make(chan capturedRequest, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request body", http.StatusInternalServerError)
			return
		}
		captured <- capturedRequest{
			body:   body,
			header: r.Header.Clone(),
			path:   r.URL.Path,
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Set-Cookie", "upstream=session")
		w.Header().Set("X-Request-ID", "upstream-request")
		w.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(w, "answer-sdp"); err != nil {
			t.Errorf("write upstream response: %v", err)
		}
	}))
	defer upstreamServer.Close()

	deps := realtimeHandlerDeps(t, &authmodel.Credential{
		Provider: "openai",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "upstream-openai-key",
		BaseURL:  upstreamServer.URL,
	})
	handler, err := NewRealtimeHandler(deps)
	require.NoError(t, err)
	router := gin.New()
	router.POST("/v1/realtime/calls", handler.HandleHandshake(upstream.OPENAI_REALTIME_CALLS_ENDPOINT))
	proxyServer := httptest.NewServer(router)
	defer proxyServer.Close()

	body := []byte("--test-boundary\r\nopaque SDP and session\r\n--test-boundary--\r\n")
	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/realtime/calls", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer downstream-proxy-key")
	req.Header.Set("x-api-key", "downstream-proxy-key")
	req.Header.Set("Cookie", "downstream=session")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test-boundary")
	req.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	req.Header.Set("X-Request-ID", "realtime-request")
	req.Header.Set(upstream.OPENAI_SAFETY_IDENTIFIER_HEADER, "privacy-preserving-user-hash")
	resp, err := proxyServer.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "answer-sdp", string(respBody))
	assert.Equal(t, "application/sdp", resp.Header.Get("Content-Type"))
	assert.Equal(t, "upstream-request", resp.Header.Get("X-Request-ID"))
	assert.Empty(t, resp.Header.Get("Set-Cookie"))

	got := <-captured
	assert.Equal(t, "/v1/realtime/calls", got.path)
	assert.Equal(t, body, got.body)
	assert.Equal(t, "Bearer upstream-openai-key", got.header.Get("Authorization"))
	assert.Empty(t, got.header.Get("x-api-key"))
	assert.Empty(t, got.header.Get("Cookie"))
	assert.Empty(t, got.header.Get("OpenAI-Beta"))
	assert.Equal(t, "multipart/form-data; boundary=test-boundary", got.header.Get("Content-Type"))
	assert.Equal(t, "privacy-preserving-user-hash", got.header.Get(upstream.OPENAI_SAFETY_IDENTIFIER_HEADER))
	assert.Equal(t, "realtime-request", got.header.Get("X-Request-ID"))
	assert.Equal(t, "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01", got.header.Get("Traceparent"))
}

func TestRealtimeHandshakeRequiresOpenAICredential(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.Realtime.Enabled = true
	cfg.Realtime.MaxConnections = 2
	cfg.Realtime.MaxHandshakeBytes = 1 << 10

	server, err := New(cfg)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls", bytes.NewBufferString("{}"))
	req.Header.Set("x-api-key", "proxy-test-key")
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.engine.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusServiceUnavailable, resp.Code)
	assert.Contains(t, resp.Body.String(), "credential")
}

func realtimeHandlerDeps(t *testing.T, credential *authmodel.Credential) RealtimeHandlerDeps {
	t.Helper()
	catalog, err := upstream.DefaultCatalog()
	require.NoError(t, err)
	store, err := utils.NewFileStore(t.TempDir())
	require.NoError(t, err)
	if credential != nil {
		require.NoError(t, store.Save(credential))
	}
	resolver := upstream.NewCredentialResolver(store, nil, func(string) (string, bool) {
		return "", false
	})
	return RealtimeHandlerDeps{
		Catalog:           catalog,
		Credentials:       resolver,
		MaxConnections:    2,
		MaxHandshakeBytes: 1 << 10,
	}
}
