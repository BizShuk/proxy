package upstream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testAdapter is a minimal provider.Adapter for dispatcher tests. The
// adapter surface is now purely capability — Generate + Stream — so a
// stub carries no identity and no catalog: the dispatcher takes the
// name from its caller and the models from the agentsdk registry.
type testAdapter struct{}

func (p *testAdapter) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	return core.ModelResult{}, nil
}

func (p *testAdapter) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	ch := make(chan core.ModelChunk)
	close(ch)
	return ch, nil
}

// Compile-time: ensure testAdapter satisfies provider.Adapter.
var _ provider.Adapter = (*testAdapter)(nil)

func newTestAdapter() *testAdapter { return &testAdapter{} }

func TestDispatcherSetLookup(t *testing.T) {
	d := NewDispatcher()
	p := newTestAdapter()
	require.NoError(t, d.Set("anthropic", p))

	got, ok := d.Lookup("anthropic")
	require.True(t, ok)
	assert.Same(t, p, got)

	_, ok = d.Lookup("unknown")
	assert.False(t, ok)
}

func TestDispatcherSetRejectsNil(t *testing.T) {
	d := NewDispatcher()
	err := d.Set("anthropic", nil)
	assert.Error(t, err)
}

func TestDispatcherSetRejectsBlankName(t *testing.T) {
	d := NewDispatcher()
	err := d.Set("", newTestAdapter())
	assert.Error(t, err)
}

func TestDispatcherSetRejectsDuplicate(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set("anthropic", newTestAdapter()))
	err := d.Set("anthropic", newTestAdapter())
	assert.Error(t, err)
}

func TestDispatcherReplace(t *testing.T) {
	d := NewDispatcher()
	p1, p2 := newTestAdapter(), newTestAdapter()
	require.NoError(t, d.Set("anthropic", p1))
	require.NoError(t, d.Replace("anthropic", p2))

	got, _ := d.Lookup("anthropic")
	assert.Same(t, p2, got)
}

func TestDispatcherIDs(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set("ollama", newTestAdapter()))
	require.NoError(t, d.Set("anthropic", newTestAdapter()))
	require.NoError(t, d.Set("grok", newTestAdapter()))

	ids := d.IDs()
	assert.Len(t, ids, 3)
	assert.ElementsMatch(t, []string{"ollama", "anthropic", "grok"}, ids)
}

// The catalog is the registry's answer, not the adapter's — a stub
// adapter registered under a real name still advertises that family's
// bundled models.
func TestDispatcherAdvertisedModelsComesFromTheRegistry(t *testing.T) {
	specs, ok := provider.Catalog("anthropic")
	require.True(t, ok)
	require.NotEmpty(t, specs)

	want := make([]string, 0, len(specs))
	for _, s := range specs {
		want = append(want, s.ID)
	}

	d := NewDispatcher()
	require.NoError(t, d.Set("anthropic", newTestAdapter()))
	assert.ElementsMatch(t, want, d.AdvertisedModels())
}

func TestDispatcherAdvertisedModelsDeduplicates(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set("anthropic", newTestAdapter()))
	require.NoError(t, d.Set("ollama", newTestAdapter()))

	got := d.AdvertisedModels()
	seen := make(map[string]struct{}, len(got))
	for _, id := range got {
		_, dup := seen[id]
		assert.Falsef(t, dup, "model %q advertised twice", id)
		seen[id] = struct{}{}
	}
}

// A family the agentsdk registry does not know contributes nothing
// rather than panicking or inventing an empty model.
func TestDispatcherAdvertisedModelsIgnoresUnregisteredNames(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set("not-a-registered-provider", newTestAdapter()))
	assert.Empty(t, d.AdvertisedModels())
}

func TestDispatcherConcurrentLookup(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set("anthropic", newTestAdapter()))

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, ok := d.Lookup("anthropic")
				if !ok {
					t.Errorf("lookup failed")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDispatcherNilSafety(t *testing.T) {
	var d *Dispatcher
	_, ok := d.Lookup("anything")
	assert.False(t, ok)
	assert.Nil(t, d.IDs())
	assert.Nil(t, d.AdvertisedModels())
	err := d.Set("test", newTestAdapter())
	assert.Error(t, err)
}

func TestErrUnknownFamily(t *testing.T) {
	assert.True(t, errors.Is(ErrUnknownFamily, ErrUnknownFamily))
	assert.True(t, strings.Contains(ErrUnknownFamily.Error(), "unknown"))
}

func TestNewDefaultDispatcherSkipsProvidersWithoutEnv(t *testing.T) {
	// All key-bearing providers require a credential via env vars; with
	// none set, each provider.New() fails and the dispatcher registers
	// only the keyless ollama adapter. This test confirms graceful
	// degradation for the key-bearing families.
	blankProviderEnv(t)

	d, err := NewDefaultDispatcher()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ollama"}, d.IDs())
}

func TestNewDefaultDispatcherRegistersWhenKeySet(t *testing.T) {
	// Set one provider's env var; the others stay blank. The dispatcher
	// should register exactly the families that successfully constructed.
	blankProviderEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	d, err := NewDefaultDispatcher()
	require.NoError(t, err)
	require.Contains(t, d.IDs(), "anthropic")
}

// NewDefaultDispatcher no longer carries its own provider list — it
// walks whatever the binary linked. provider/all is blank-imported, so
// every canonical family must be visible.
func TestNewDefaultDispatcherSeesEveryLinkedFamily(t *testing.T) {
	assert.Subset(t, provider.Names(), []string{
		"anthropic", "antigravity", "codex", "google", "grok", "minimax", "ollama",
	})
}
