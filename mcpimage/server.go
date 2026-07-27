package mcpimage

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	SERVER_NAME              = "proxy-imagegen"
	SERVER_VERSION           = "0.1.0"
	TOOL_NAME_GENERATE_IMAGE = "generate_image"
)

// NewServer returns a stdio-ready MCP server with the generate_image tool.
func NewServer(generator *Generator) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: SERVER_NAME, Version: SERVER_VERSION},
		&mcp.ServerOptions{
			Instructions: "Use generate_image when the user asks to create or generate an image. " +
				"The tool calls the configured local proxy, incurs provider cost, saves files under " +
				"the current project, and returns both image content and saved paths.",
		},
	)
	destructive := false
	openWorld := true
	mcp.AddTool(server, &mcp.Tool{
		Name:  TOOL_NAME_GENERATE_IMAGE,
		Title: "Generate image through proxy",
		Description: "Generate one or more images through the configured proxy, " +
			"save them under the project, and return the images plus file paths.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Generate image through proxy",
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
			ReadOnlyHint:    false,
		},
	}, generator.Generate)
	return server
}

// Run serves MCP over stdin/stdout until the client disconnects.
func Run(ctx context.Context, generator *Generator) error {
	return NewServer(generator).Run(ctx, &mcp.StdioTransport{})
}
