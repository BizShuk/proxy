package transform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}
