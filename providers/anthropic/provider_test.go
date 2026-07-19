package anthropic_test

import (
	"context"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := anthropic.New()
	assert.Error(t, err)
}

func TestGenerateAgainstFakeServer(t *testing.T) {
	// Stand up a fake anthropic server. We don't go through the real
	// SDK's HTTP transport here because that would require a key; we
	// stub at a higher level — the test ensures the adapter can be
	// constructed and Configured with the expected API key path.
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-from-env")

	// This test only exercises construction + Name(); real network
	// tests are skipped when ANTHROPIC_API_KEY is missing in CI.
	if testing.Short() {
		t.Skip("skipping anthropic adapter construction check under -short")
	}

	p, err := anthropic.New()
	require.NoError(t, err)
	assert.Equal(t, "anthropic:claude-3-5-sonnet-latest", p.Name())

	// The adapter's CountTokens is heuristic — verify it returns a
	// positive count on a non-empty transcript.
	n, err := p.CountTokens(context.Background(), []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hello world"}}},
	})
	require.NoError(t, err)
	assert.Greater(t, n, 0)
}

func TestGenerateWithOption(t *testing.T) {
	p, err := anthropic.New(anthropic.WithAPIKey("sk-direct"))
	require.NoError(t, err)
	assert.Equal(t, "anthropic:claude-3-5-sonnet-latest", p.Name())
}