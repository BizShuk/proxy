package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdapter satisfies provider.Adapter for the dispatcher integration
// test. The adapter contract is capability-only (Generate + Stream);
// identity and catalog come from the agentsdk registry, so this stub
// deliberately carries neither.
type fakeAdapter struct{}

func (f *fakeAdapter) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	return core.ModelResult{}, nil
}
func (f *fakeAdapter) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	ch := make(chan core.ModelChunk)
	close(ch)
	return ch, nil
}

var _ provider.Adapter = (*fakeAdapter)(nil)

// catalogIDs is the model-id set agentsdk bundles for a family — the
// same source /v1/models reads through the dispatcher.
func catalogIDs(t *testing.T, names ...string) []string {
	t.Helper()
	seen := make(map[string]struct{})
	var out []string
	for _, name := range names {
		specs, ok := provider.Catalog(name)
		require.Truef(t, ok, "provider %q must be registered", name)
		require.NotEmptyf(t, specs, "provider %q must bundle a catalog", name)
		for _, s := range specs {
			if _, dup := seen[s.ID]; dup {
				continue
			}
			seen[s.ID] = struct{}{}
			out = append(out, s.ID)
		}
	}
	sort.Strings(out)
	return out
}

func TestHandleModelsFallsBackToCatalogWhenNoDispatcher(t *testing.T) {
	// Existing test path: no Dispatcher wired → falls back to catalog
	// (TestHandlerModelsUsesCatalog covers this).
	deps := newHandlerDeps(t, nil)
	handler, err := NewHandler(deps)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/models", handler.HandleModels())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/models", nil))

	require.Equal(t, http.StatusOK, resp.Code)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	assert.NotEmpty(t, payload.Data, "catalog fallback must return at least one model")
}

func TestHandleModelsUsesDispatcherWhenWired(t *testing.T) {
	// Dispatcher is wired → /v1/models serves the union of the registry
	// catalogs for the registered families, deduplicated.
	d := upstream.NewDispatcher()
	require.NoError(t, d.Set("anthropic", &fakeAdapter{}))
	require.NoError(t, d.Set("ollama", &fakeAdapter{}))

	deps := newHandlerDeps(t, nil)
	deps.Dispatcher = d
	handler, err := NewHandler(deps)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/models", handler.HandleModels())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/models", nil))

	require.Equal(t, http.StatusOK, resp.Code)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))

	var ids []string
	for _, item := range payload.Data {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	assert.Equal(t, catalogIDs(t, "anthropic", "ollama"), ids,
		"dispatcher must serve the registry catalogs, deduped across families")
}

func TestDispatcherLookupReturnsProviderAdapter(t *testing.T) {
	d := upstream.NewDispatcher()
	fa := &fakeAdapter{}
	require.NoError(t, d.Set("anthropic", fa))

	got, ok := d.Lookup("anthropic")
	require.True(t, ok)
	assert.Same(t, fa, got)

	// Generate/Stream are the whole contract now — assert the adapter is
	// usable through the interface the dispatcher hands back.
	_, err := got.Generate(context.Background(), core.ModelRequest{})
	require.NoError(t, err)
	ch, err := got.Stream(context.Background(), core.ModelRequest{})
	require.NoError(t, err)
	_, open := <-ch
	assert.False(t, open)
}
