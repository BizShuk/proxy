package mcpimage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyImagegenPluginContract(t *testing.T) {
	pluginRoot := filepath.Join("..", "plugins", "proxy-imagegen")

	codexManifest := readJSONObject(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"))
	assert.Equal(t, "proxy-imagegen", codexManifest["name"])
	assert.Equal(t, "./skills/", codexManifest["skills"])
	codexServers, ok := codexManifest["mcpServers"].(map[string]any)
	require.True(t, ok)
	codexServer, ok := codexServers["proxy-imagegen"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "proxy", codexServer["command"])
	assert.Equal(t, []any{"image-mcp"}, codexServer["args"])
	assert.ElementsMatch(t, []any{
		"PROXY_IMAGE_BASE_URL",
		"PROXY_IMAGE_PORT",
		"PROXY_IMAGE_API_KEY",
		"AGENTSDK_PROXY_API_KEY",
		"PROXY_IMAGE_MODEL",
		"PROXY_IMAGE_OUTPUT_DIR",
	}, codexServer["env_vars"])

	claudeManifest := readJSONObject(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"))
	assert.Equal(t, "proxy-imagegen", claudeManifest["name"])
	assert.Equal(t, "./.mcp.json", claudeManifest["mcpServers"])
	userConfig, ok := claudeManifest["userConfig"].(map[string]any)
	require.True(t, ok)
	for _, name := range []string{"base_url", "port", "api_key", "model"} {
		assert.Contains(t, userConfig, name)
	}

	claudeMCP := readJSONObject(t, filepath.Join(pluginRoot, ".mcp.json"))
	servers, ok := claudeMCP["mcpServers"].(map[string]any)
	require.True(t, ok)
	claudeServer, ok := servers["proxy-imagegen"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "proxy", claudeServer["command"])
	assert.Equal(t, []any{"image-mcp"}, claudeServer["args"])
	claudeEnv, ok := claudeServer["env"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "images", claudeEnv["PROXY_IMAGE_OUTPUT_DIR"])
	assert.Equal(t, "${CLAUDE_PROJECT_DIR}", claudeEnv["CLAUDE_PROJECT_DIR"])

	skill, err := os.ReadFile(filepath.Join(pluginRoot, "skills", "imagine", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(skill), "generate_image")
	assert.Contains(t, string(skill), "$ARGUMENTS")
}

func readJSONObject(t *testing.T, path string) map[string]any {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(body, &value))
	return value
}
