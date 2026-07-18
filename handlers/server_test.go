package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pxconfig "github.com/bizshuk/proxy/config"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServerWiresGenericHandlerRoutes(t *testing.T) {
	server, err := New(testProxyConfig(t))
	require.NoError(t, err)
	require.NotNil(t, server)

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("x-api-key", "proxy-test-key")
	response := httptest.NewRecorder()
	server.engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"object":"list"`)
	assert.NotContains(t, response.Body.String(), "not implemented")
}

func TestNewServerRejectsInvalidConfiguration(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.BodyLimit = 0
	server, err := New(cfg)
	require.Error(t, err)
	assert.Nil(t, server)
}

func TestNewServerRejectsBodyLimitThatOverflowsBytes(t *testing.T) {
	cfg := testProxyConfig(t)
	cfg.BodyLimit = (1 << 44) + 1

	server, err := New(cfg)

	require.Error(t, err)
	assert.Nil(t, server)
}

func TestNewHTTPServerUsesStreamingSafeTimeouts(t *testing.T) {
	handler := http.NewServeMux()

	server := newHTTPServer("127.0.0.1:8317", handler)

	assert.Equal(t, "127.0.0.1:8317", server.Addr)
	assert.Same(t, handler, server.Handler)
	assert.Equal(t, 10*time.Second, server.ReadHeaderTimeout)
	assert.Equal(t, 2*time.Minute, server.IdleTimeout)
	assert.Equal(t, 1<<20, server.MaxHeaderBytes)
	assert.Zero(t, server.WriteTimeout)
}

func testProxyConfig(t *testing.T) *pxconfig.Config {
	t.Helper()
	return &pxconfig.Config{
		AuthDir: t.TempDir(), APIKeys: []string{"proxy-test-key"}, BodyLimit: 1,
		Timeouts: upstream.TimeoutConfig{MessagesMs: 1000, StreamMessagesMs: 1000, CountTokensMs: 1000},
	}
}
