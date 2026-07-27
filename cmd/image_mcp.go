package cmd

import (
	"fmt"
	"net/http"
	"os"

	"github.com/bizshuk/proxy/mcpimage"
	"github.com/spf13/cobra"
)

var (
	imageMCPBaseURL   = mcpimage.DEFAULT_BASE_URL
	imageMCPOutputDir = mcpimage.DEFAULT_OUTPUT_DIR
	imageMCPModel     = mcpimage.DEFAULT_MODEL

	// ImageMCPCmd serves the proxy image generator as a local stdio MCP server.
	ImageMCPCmd = &cobra.Command{
		Use:   "image-mcp",
		Short: "Serve proxy image generation over MCP stdio",
		Long: "Serve proxy image generation over MCP stdio.\n\n" +
			"Set PROXY_IMAGE_API_KEY or AGENTSDK_PROXY_API_KEY in the server environment. " +
			"Base URL, port, model, and output directory can also be set with PROXY_IMAGE_* " +
			"environment variables.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := mcpimage.LoadConfigFromEnv(os.LookupEnv)
			if err != nil {
				return fmt.Errorf("load image MCP config: %w", err)
			}
			if cmd.Flag("base-url").Changed {
				cfg.BaseURL = imageMCPBaseURL
			}
			if cmd.Flag("port").Changed {
				cfg.Port = port
			}
			if cmd.Flag("output-dir").Changed {
				cfg.OutputDir = imageMCPOutputDir
			}
			if cmd.Flag("model").Changed {
				cfg.Model = imageMCPModel
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			client, err := mcpimage.NewProxyClient(cfg, http.DefaultClient)
			if err != nil {
				return fmt.Errorf("create image MCP proxy client: %w", err)
			}
			generator, err := mcpimage.NewGenerator(client, cfg)
			if err != nil {
				return fmt.Errorf("create image MCP generator: %w", err)
			}
			return mcpimage.Run(cmd.Context(), generator)
		},
	}
)

func init() {
	ImageMCPCmd.Flags().StringVar(
		&imageMCPBaseURL,
		"base-url",
		mcpimage.DEFAULT_BASE_URL,
		"Proxy base URL without the port",
	)
	ImageMCPCmd.Flags().StringVar(
		&imageMCPOutputDir,
		"output-dir",
		mcpimage.DEFAULT_OUTPUT_DIR,
		"Project-relative or absolute image output directory",
	)
	ImageMCPCmd.Flags().StringVar(
		&imageMCPModel,
		"model",
		mcpimage.DEFAULT_MODEL,
		"Default image generation model",
	)
	ProxyCmd.AddCommand(ImageMCPCmd)
}
