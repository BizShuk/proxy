package transform

import (
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/require"
)

func TestDefaultRegistryCoversMatrix(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)

	for _, from := range model.ALL_FORMATS {
		for _, to := range model.ALL_FORMATS {
			pair, ok := registry.Lookup(from, to)
			require.Truef(t, ok, "%s -> %s", from, to)
			require.NotNil(t, pair.Request)
			require.NotNil(t, pair.Response)
			require.NotNil(t, pair.NewStream)
		}
	}
}
