package transform

import (
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/require"
)

func TestDefaultRegistryCoversMatrix(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)

	for _, from := range model.CLIENT_FORMATS {
		for _, to := range model.CLIENT_FORMATS {
			pair, ok := registry.Lookup(from, to)
			require.Truef(t, ok, "%s -> %s", from, to)
			require.NotNil(t, pair.Request)
			require.NotNil(t, pair.Response)
			require.NotNil(t, pair.NewStream)
		}
	}
}

// Provider-only formats are targets, not sources: they need a client that can
// reach them, and must never be registered as the "from" side of a pair.
func TestDefaultRegistryReachesProviderFormats(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)

	for _, to := range model.PROVIDER_FORMATS {
		reachable := 0
		for _, from := range model.CLIENT_FORMATS {
			if _, ok := registry.Lookup(from, to); ok {
				reachable++
			}
			_, ok := registry.Lookup(to, from)
			require.Falsef(t, ok, "%s must not be a transform source", to)
		}
		require.Positivef(t, reachable, "%s is unreachable from every client format", to)
	}

	pair, ok := registry.Lookup(model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_ANTIGRAVITY)
	require.True(t, ok)
	require.NotNil(t, pair.Request)
	require.NotNil(t, pair.Response)
	require.NotNil(t, pair.NewStream)
}
