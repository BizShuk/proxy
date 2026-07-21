package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyCmdExposesPortFlag(t *testing.T) {
	flag := ProxyCmd.PersistentFlags().Lookup("port")

	if assert.NotNil(t, flag) {
		assert.Equal(t, "8317", flag.DefValue)
		assert.Equal(t, "Server port", flag.Usage)
	}
}

func TestProxyCommandUsesPackageLevelStyle(t *testing.T) {
	source, err := os.ReadFile("proxy.go")
	require.NoError(t, err)

	assert.NotContains(t, string(source), "func NewCommand")
	assert.NotContains(t, string(source), `Changed("port")`)
	assert.NotContains(t, string(source), "addCommonFlags")
	assert.Contains(t, string(source), "ProxyCmd = &cobra.Command")
	assert.Contains(t, string(source), "func init()")
	assert.Contains(t, string(source), `ProxyCmd.PersistentFlags().IntVar(&port, "port", DEFAULT_PORT, "Server port")`)
	assert.Contains(t, strings.ReplaceAll(string(source), " ", ""), "cfg.Server.Port=port")
}
