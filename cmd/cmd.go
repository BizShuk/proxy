// Package cmd exposes the proxy server as a reusable cobra command, so the
// proxy module carries its own CLI surface: the aggregated auth-cli mounts
// it, and a standalone binary can do the same.
package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	pxconfig "github.com/bizshuk/proxy/config"
	"github.com/bizshuk/proxy/handlers"
	"github.com/spf13/cobra"
)

const DEFAULT_PORT = 8317

// NewCommand returns the `proxy` command. It is self-contained: settings come
// from pxconfig.LoadConfig (gosdk layered viper under the agentSDK namespace).
func NewCommand() *cobra.Command {
	port := DEFAULT_PORT
	command := &cobra.Command{
		Use:   "proxy",
		Short: "Start the proxy server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := pxconfig.LoadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			cfg.Server.Port = port

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			server, err := handlers.New(cfg)
			if err != nil {
				return fmt.Errorf("create proxy server: %w", err)
			}
			return server.Run(ctx)
		},
	}
	command.PersistentFlags().IntVar(&port, "port", DEFAULT_PORT, "Server port")
	return command
}
