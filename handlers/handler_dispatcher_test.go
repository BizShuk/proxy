package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeProvider satisfies core.Provider for the dispatcher integration test.
type fakeProvider struct {
	idVal  string
	models []core.ModelSpec
}

func (f *fakeProvider) ID() string               { return f.idVal }
func (f *fakeProvider) Models() []core.ModelSpec { return f.models }
func (f *fakeProvider) AuthSchemes() []string    { return []string{"api_key"} }
func (f *fakeProvider) Generate(ctx context.Context, req core.ModelRequest) (core.ModelResult, error) {
	return core.ModelResult{}, nil
}
func (f *fakeProvider) Stream(ctx context.Context, req core.ModelRequest) (<-chan core.ModelChunk, error) {
	ch := make(chan core.ModelChunk)
	close(ch)
	return ch, nil
}
func (f *fakeProvider) CountTokens(ctx context.Context, msgs []core.Message) (int, error) {
	return 0, nil
}

var _ core.Provider = (*fakeProvider)(nil)

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
	// Should have at least one model id (catalog returns prefixes/IDs).
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &payload))
	assert.NotEmpty(t, payload.Data, "catalog fallback must return at least one model")
}

func TestHandleModelsUsesDispatcherWhenWired(t *testing.T) {
	// New path: Dispatcher is wired → /v1/models serves the union of
	// the provider catalogs.
	d, err := upstream.NewDispatcherFromProviders(
		&fakeProvider{idVal: "anthropic", models: []core.ModelSpec{
			{ID: "claude-opus-4-8"},
			{ID: "claude-sonnet-5"},
		}},
		&fakeProvider{idVal: "ollama", models: []core.ModelSpec{
			{ID: "llama3.2"},
			{ID: "claude-opus-4-8"}, // duplicate id across providers — must dedup
		}},
	)
	require.NoError(t, err)

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
	assert.Equal(t, []string{"claude-opus-4-8", "claude-sonnet-5", "llama3.2"}, ids,
		"dispatcher must dedup cross-provider model ids")
}

func TestDispatcherLookupReturnsCoreProviderInterface(t *testing.T) {
	d := upstream.NewDispatcher()
	fp := &fakeProvider{idVal: "anthropic", models: []core.ModelSpec{{ID: "claude-opus-4-8"}}}
	require.NoError(t, d.Set(fp))

	got, ok := d.Lookup("anthropic")
	require.True(t, ok)
	assert.Equal(t, "anthropic", got.ID())
	assert.Equal(t, []string{"api_key"}, got.AuthSchemes())
	assert.Len(t, got.Models(), 1)
}
