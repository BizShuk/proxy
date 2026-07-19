package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCommandExposesPortFlag(t *testing.T) {
	command := NewCommand()

	flag := command.PersistentFlags().Lookup("port")

	if assert.NotNil(t, flag) {
		assert.Equal(t, "8317", flag.DefValue)
		assert.Equal(t, "Server port", flag.Usage)
	}
}

func TestCommandScopeKeepsPortFlagInline(t *testing.T) {
	source, err := os.ReadFile("cmd.go")
	require.NoError(t, err)

	assert.NotContains(t, string(source), `Changed("port")`)
	assert.NotContains(t, string(source), "addCommonFlags")
	assert.Contains(t, string(source), `PersistentFlags().IntVar(&opts.port, "port", DEFAULT_PORT, "Server port")`)
	assert.Contains(t, strings.ReplaceAll(string(source), " ", ""), "cfg.Server.Port=opts.port")
}
