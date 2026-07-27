package cmd

import (
	"fmt"
	"io"

	"github.com/bizshuk/agentsdk/provider/codex"
	"github.com/bizshuk/proxy/mcpimage"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/spf13/cobra"
)

// OptionsCmd lists the model options currently exposed by the proxy.
var OptionsCmd = &cobra.Command{
	Use:     "options",
	Aliases: []string{"models"},
	Short:   "List available model options",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return writeModelOptions(cmd.OutOrStdout())
	},
}

func init() {
	ProxyCmd.AddCommand(OptionsCmd)
}

func writeModelOptions(w io.Writer) error {
	if w == nil {
		return fmt.Errorf("write model options: output is required")
	}

	fmt.Fprintln(w, "Codex models:")
	for _, spec := range codex.DefaultCatalog() {
		if spec.ID != "" {
			fmt.Fprintf(w, "  %s\n", spec.ID)
		}
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "OpenAI image-generation models:")
	fmt.Fprintf(w, "  %s\n", upstream.OPENAI_IMAGE_DEFAULT_MODEL)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Grok image-generation models:")
	fmt.Fprintf(w, "  %s\n", mcpimage.DEFAULT_MODEL)
	return nil
}
