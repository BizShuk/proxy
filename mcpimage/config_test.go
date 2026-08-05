package mcpimage

import (
	"testing"

	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultModelUsesGrokUpstreamCatalogValue(t *testing.T) {
	assert.Equal(t, sdkgrok.DefaultImageModel, DEFAULT_MODEL)
}

func TestLoadConfigFromEnvUsesDefaultsAndProxyAPIKey(t *testing.T) {
	cfg, err := LoadConfigFromEnv(mapLookup(map[string]string{
		"PROXY_IMAGE_API_KEY": "proxy-secret",
	}))

	require.NoError(t, err)
	assert.Equal(t, DEFAULT_BASE_URL, cfg.BaseURL)
	assert.Equal(t, DEFAULT_PORT, cfg.Port)
	assert.Equal(t, "proxy-secret", cfg.APIKey)
	assert.Equal(t, DEFAULT_MODEL, cfg.Model)
	assert.Equal(t, DEFAULT_OUTPUT_DIR, cfg.OutputDir)
}

func TestLoadConfigFromEnvAcceptsCustomProxyConnection(t *testing.T) {
	cfg, err := LoadConfigFromEnv(mapLookup(map[string]string{
		"PROXY_IMAGE_BASE_URL":   "https://proxy.example.test/gateway/v1",
		"PROXY_IMAGE_PORT":       "9443",
		"PROXY_IMAGE_API_KEY":    "custom-secret",
		"PROXY_IMAGE_MODEL":      "custom-image-model",
		"PROXY_IMAGE_OUTPUT_DIR": "artifacts/generated",
	}))

	require.NoError(t, err)
	assert.Equal(t, "https://proxy.example.test/gateway/v1", cfg.BaseURL)
	assert.Equal(t, 9443, cfg.Port)
	assert.Equal(t, "custom-secret", cfg.APIKey)
	assert.Equal(t, "custom-image-model", cfg.Model)
	assert.Equal(t, "artifacts/generated", cfg.OutputDir)
}

func TestLoadConfigFromEnvFallsBackToAgentSDKProxyAPIKey(t *testing.T) {
	cfg, err := LoadConfigFromEnv(mapLookup(map[string]string{
		"AGENTSDK_PROXY_API_KEY": "agentsdk-secret",
	}))

	require.NoError(t, err)
	assert.Equal(t, "agentsdk-secret", cfg.APIKey)
}

func TestLoadConfigFromEnvRejectsInvalidPort(t *testing.T) {
	_, err := LoadConfigFromEnv(mapLookup(map[string]string{
		"PROXY_IMAGE_API_KEY": "proxy-secret",
		"PROXY_IMAGE_PORT":    "not-a-port",
	}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "PROXY_IMAGE_PORT")
}

func TestConfigEndpointURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		port    int
		want    string
	}{
		{
			name:    "host only",
			baseURL: "http://127.0.0.1",
			port:    8317,
			want:    "http://127.0.0.1:8317/v1/images/generations",
		},
		{
			name:    "replace existing port",
			baseURL: "https://proxy.example.test:443",
			port:    9443,
			want:    "https://proxy.example.test:9443/v1/images/generations",
		},
		{
			name:    "preserve gateway and v1 path",
			baseURL: "https://proxy.example.test/gateway/v1",
			port:    9443,
			want:    "https://proxy.example.test:9443/gateway/v1/images/generations",
		},
		{
			name:    "IPv6",
			baseURL: "http://[::1]",
			port:    8317,
			want:    "http://[::1]:8317/v1/images/generations",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				BaseURL:   tc.baseURL,
				Port:      tc.port,
				APIKey:    "proxy-secret",
				Model:     DEFAULT_MODEL,
				OutputDir: DEFAULT_OUTPUT_DIR,
			}

			endpoint, err := cfg.EndpointURL()

			require.NoError(t, err)
			assert.Equal(t, tc.want, endpoint)
		})
	}
}

func TestConfigEndpointURLRejectsUnsafeBaseURL(t *testing.T) {
	tests := []string{
		"ftp://proxy.example.test",
		"http://user:secret@proxy.example.test",
		"http://proxy.example.test?token=secret",
		"/relative/path",
	}

	for _, baseURL := range tests {
		t.Run(baseURL, func(t *testing.T) {
			cfg := Config{
				BaseURL:   baseURL,
				Port:      DEFAULT_PORT,
				APIKey:    "proxy-secret",
				Model:     DEFAULT_MODEL,
				OutputDir: DEFAULT_OUTPUT_DIR,
			}

			_, err := cfg.EndpointURL()

			require.Error(t, err)
		})
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
