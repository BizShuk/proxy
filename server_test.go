package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bizshuk/agentsdk/config"
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

func testProxyConfig(t *testing.T) *config.ProxyConfig {
	t.Helper()
	return &config.ProxyConfig{
		AuthDir: t.TempDir(), APIKeys: []string{"proxy-test-key"}, BodyLimit: 1,
		Timeouts: config.ProxyTimeoutConfig{MessagesMs: 1000, StreamMessagesMs: 1000, CountTokensMs: 1000},
	}
}
