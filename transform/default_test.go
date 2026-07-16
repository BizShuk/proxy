package transform

import (
	"testing"

	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/require"
)

func TestDefaultRegistryCoversMatrix(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)

	for _, from := range protocol.ALL_FORMATS {
		for _, to := range protocol.ALL_FORMATS {
			pair, ok := registry.Lookup(from, to)
			require.Truef(t, ok, "%s -> %s", from, to)
			require.NotNil(t, pair.Request)
			require.NotNil(t, pair.Response)
			require.NotNil(t, pair.NewStream)
		}
	}
}
