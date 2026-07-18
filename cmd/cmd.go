// Package cmd exposes the proxy server as a reusable cobra command, so the
// proxy module carries its own CLI surface: the aggregated auth-cli mounts
// it, and a standalone binary can do the same.
package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/bizshuk/proxy/proxy"
	"github.com/spf13/cobra"
)

// NewCommand returns the `proxy` command. It is self-contained: settings come
// from proxy.LoadConfig (gosdk layered viper under the agentSDK namespace).
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "proxy",
		Short: "Start the proxy server",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run()
		},
	}
}

func run() error {
	cfg, err := proxy.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server, err := proxy.New(cfg)
	if err != nil {
		return fmt.Errorf("create proxy server: %w", err)
	}
	return server.Run(ctx)
}
