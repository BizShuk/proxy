package upstream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testProvider is a minimal core.Provider for dispatcher tests. It
// reports a fixed ID + model catalog and returns zero results from
// Generate/Stream/CountTokens.
type testProvider struct {
	id     string
	models []core.ModelSpec
}

func (p *testProvider) ID() string               { return p.id }
func (p *testProvider) Models() []core.ModelSpec { return p.models }
func (p *testProvider) AuthSchemes() []string    { return []string{"api_key"} }

func (p *testProvider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	return core.ModelResult{}, nil
}

func (p *testProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	ch := make(chan core.ModelChunk)
	close(ch)
	return ch, nil
}

func (p *testProvider) CountTokens(ctx context.Context, msgs []core.Message) (int, error) {
	return 0, nil
}

// Compile-time: ensure testProvider satisfies core.Provider.
var _ core.Provider = (*testProvider)(nil)

func newTestProvider(id string, models []core.ModelSpec) *testProvider {
	return &testProvider{id: id, models: models}
}

func TestDispatcherSetLookup(t *testing.T) {
	d := NewDispatcher()
	p := newTestProvider("anthropic", []core.ModelSpec{{ID: "claude-opus-4-8"}})
	require.NoError(t, d.Set(p))

	got, ok := d.Lookup("anthropic")
	require.True(t, ok)
	assert.Equal(t, "anthropic", got.ID())
	assert.Equal(t, "claude-opus-4-8", got.Models()[0].ID)

	_, ok = d.Lookup("unknown")
	assert.False(t, ok)
}

func TestDispatcherSetRejectsNil(t *testing.T) {
	d := NewDispatcher()
	err := d.Set(nil)
	assert.Error(t, err)
}

func TestDispatcherSetRejectsBlankID(t *testing.T) {
	d := NewDispatcher()
	p := &testProvider{id: "", models: nil}
	err := d.Set(p)
	assert.Error(t, err)
}

func TestDispatcherSetRejectsDuplicate(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set(newTestProvider("anthropic", nil)))
	err := d.Set(newTestProvider("anthropic", nil))
	assert.Error(t, err)
}

func TestDispatcherReplace(t *testing.T) {
	d := NewDispatcher()
	p1 := newTestProvider("anthropic", []core.ModelSpec{{ID: "old"}})
	p2 := newTestProvider("anthropic", []core.ModelSpec{{ID: "new"}})
	require.NoError(t, d.Set(p1))
	require.NoError(t, d.Replace("anthropic", p2))

	got, _ := d.Lookup("anthropic")
	assert.Equal(t, "new", got.Models()[0].ID)
}

func TestDispatcherIDs(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set(newTestProvider("ollama", nil)))
	require.NoError(t, d.Set(newTestProvider("anthropic", nil)))
	require.NoError(t, d.Set(newTestProvider("grok", nil)))

	ids := d.IDs()
	assert.Len(t, ids, 3)
	assert.ElementsMatch(t, []string{"ollama", "anthropic", "grok"}, ids)
}

func TestDispatcherAdvertisedModelsDeduplicates(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set(newTestProvider("anthropic", []core.ModelSpec{
		{ID: "claude-opus-4-8"},
	})))
	require.NoError(t, d.Set(newTestProvider("ollama", []core.ModelSpec{
		{ID: "claude-opus-4-8"}, // cross-provider collision; tests dedup
		{ID: "llama3.2"},
	})))

	got := d.AdvertisedModels()
	assert.ElementsMatch(t, []string{"claude-opus-4-8", "llama3.2"}, got)
}

func TestDispatcherConcurrentLookup(t *testing.T) {
	d := NewDispatcher()
	require.NoError(t, d.Set(newTestProvider("anthropic", nil)))

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
	err := d.Set(newTestProvider("test", nil))
	assert.Error(t, err)
}

func TestNewDispatcherFromProviders(t *testing.T) {
	providers := []core.Provider{
		newTestProvider("anthropic", nil),
		newTestProvider("ollama", nil),
	}
	d, err := NewDispatcherFromProviders(providers...)
	require.NoError(t, err)
	assert.Len(t, d.IDs(), 2)
}

func TestNewDispatcherFromProvidersRejectsNil(t *testing.T) {
	_, err := NewDispatcherFromProviders(nil)
	assert.Error(t, err)
}

func TestNewDispatcherFromProvidersRejectsDuplicate(t *testing.T) {
	_, err := NewDispatcherFromProviders(
		newTestProvider("anthropic", nil),
		newTestProvider("anthropic", nil),
	)
	assert.Error(t, err)
}

func TestErrUnknownFamily(t *testing.T) {
	assert.True(t, errors.Is(ErrUnknownFamily, ErrUnknownFamily))
	assert.True(t, strings.Contains(ErrUnknownFamily.Error(), "unknown"))
}

func TestNewDefaultDispatcherSkipsProvidersWithoutEnv(t *testing.T) {
	// All key-bearing providers require API keys via env vars; with no
	// env set, each provider.New() fails and the dispatcher registers
	// only the keyless ollama provider. This test confirms graceful
	// degradation for the key-bearing families.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("ANTIGRAVITY_API_KEY", "")

	d, err := NewDefaultDispatcher()
	require.NoError(t, err)
	ids := d.IDs()
	// Ollama is keyless → it's the only registration.
	assert.ElementsMatch(t, []string{"ollama"}, ids)
}

func TestNewDefaultDispatcherRegistersWhenKeySet(t *testing.T) {
	// Set one provider's env var; the others stay blank. The dispatcher
	// should register exactly the providers that successfully constructed.
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("ANTIGRAVITY_API_KEY", "")

	d, err := NewDefaultDispatcher()
	require.NoError(t, err)
	ids := d.IDs()
	require.Contains(t, ids, "anthropic")
}
