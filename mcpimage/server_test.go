package mcpimage

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerExposesGenerateImageToolOverMCP(t *testing.T) {
	client := &recordingImageClient{
		images: []GeneratedImage{{Data: testPNGBytes(), MIMEType: "image/png"}},
	}
	generator, err := NewGenerator(client, Config{
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	server := NewServer(generator)

	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	mcpClient := mcp.NewClient(
		&mcp.Implementation{Name: "proxy-imagegen-test", Version: "v0.0.1"},
		nil,
	)
	clientSession, err := mcpClient.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	tools, err := clientSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, TOOL_NAME_GENERATE_IMAGE, tools.Tools[0].Name)

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: TOOL_NAME_GENERATE_IMAGE,
		Arguments: map[string]any{
			"prompt": "a cat in space",
		},
	})

	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.Len(t, result.Content, 2)
	_, ok := result.Content[0].(*mcp.TextContent)
	assert.True(t, ok)
	image, ok := result.Content[1].(*mcp.ImageContent)
	require.True(t, ok)
	assert.Equal(t, testPNGBytes(), image.Data)
}
