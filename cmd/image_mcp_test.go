package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyCommandExposesImageMCPSubcommand(t *testing.T) {
	command, _, err := ProxyCmd.Find([]string{"image-mcp"})

	require.NoError(t, err)
	assert.Equal(t, "image-mcp", command.Name())
	assert.Equal(t, "http://127.0.0.1", command.Flag("base-url").DefValue)
	assert.Equal(t, "images", command.Flag("output-dir").DefValue)
	assert.Equal(t, "grok-imagine-image-quality", command.Flag("model").DefValue)
	assert.Nil(t, command.Flag("api-key"))
	assert.Contains(t, command.Long, "PROXY_IMAGE_API_KEY")
	assert.Contains(t, command.Long, "AGENTSDK_PROXY_API_KEY")
}
