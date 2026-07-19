package ollama_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/ollama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeOllama stands up a minimal HTTP server that pretends to be
// Ollama / an OpenAI-compatible endpoint. The handler inspects the
// /chat/completions request body and returns a small canned response.
func newFakeOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "id": "test-id",
		  "choices": [
		    {"message": {"role": "assistant", "content": "hello from ollama"}, "finish_reason": "stop"}
		  ],
		  "usage": {"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderRoundTripAgainstFakeOllama(t *testing.T) {
	srv := newFakeOllama(t)
	p, err := ollama.New(ollama.WithBaseURL(srv.URL))
	require.NoError(t, err)
	assert.Equal(t, "ollama:llama3.2", p.Name())
	assert.Equal(t, "ollama", p.ID())

	req := core.ModelRequest{Messages: []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}},
	}}
	mr, err := p.Generate(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "hello from ollama", mr.Text)
	assert.Equal(t, "stop", mr.StopReason)
	assert.Equal(t, 9, mr.Usage.TotalTokens)
}

func TestProviderIncludesBearerHeader(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p, err := ollama.New(ollama.WithBaseURL(srv.URL), ollama.WithAPIKey("sk-test"))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.True(t, strings.Contains(sawAuth, "Bearer sk-test"))
}

func TestProviderPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"rate-limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p, err := ollama.New(ollama.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestProviderSkipsBearerForLocalHost(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p, err := ollama.New(ollama.WithBaseURL(srv.URL), ollama.WithAPIKey(""))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.Empty(t, sawAuth)
}

func TestRequestBodyValidate(t *testing.T) {
	// empty model — must fail
	err := ollama.RequestBody{Messages: []ollama.ChatMessage{{Role: "user"}}}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")

	// empty messages — must fail
	err = ollama.RequestBody{Model: "llama3.2"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one message")

	// bad role — must fail
	err = ollama.RequestBody{
		Model:    "llama3.2",
		Messages: []ollama.ChatMessage{{Role: "bogus"}},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "role")

	// happy path — all four roles pass
	for _, role := range []string{"system", "user", "assistant", "tool"} {
		err = ollama.RequestBody{
			Model:    "llama3.2",
			Messages: []ollama.ChatMessage{{Role: role}},
		}.Validate()
		assert.NoError(t, err, "role %q should validate", role)
	}
}

func TestAuthResolvers(t *testing.T) {
	t.Run("ResolveAPIKey prefers explicit over env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "env-key")
		assert.Equal(t, "explicit-key", ollama.ResolveAPIKey("explicit-key"))
	})

	t.Run("ResolveAPIKey falls back to env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "env-key")
		assert.Equal(t, "env-key", ollama.ResolveAPIKey(""))
	})

	t.Run("ResolveAPIKey empty when both are empty", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		assert.Equal(t, "", ollama.ResolveAPIKey(""))
	})

	t.Run("ResolveBaseURL prefers explicit over env", func(t *testing.T) {
		t.Setenv("OPENAI_BASE_URL", "http://env-host/v1")
		assert.Equal(t, "http://explicit/v1", ollama.ResolveBaseURL("http://explicit/v1"))
	})

	t.Run("ResolveBaseURL falls back to env", func(t *testing.T) {
		t.Setenv("OPENAI_BASE_URL", "http://env-host/v1")
		assert.Equal(t, "http://env-host/v1", ollama.ResolveBaseURL(""))
	})

	t.Run("ResolveBaseURL defaults to local Ollama", func(t *testing.T) {
		t.Setenv("OPENAI_BASE_URL", "")
		assert.Equal(t, "http://localhost:11434/v1", ollama.ResolveBaseURL(""))
	})
}

func TestProviderAuthSchemes(t *testing.T) {
	p, err := ollama.New(ollama.WithBaseURL("http://example/v1"))
	require.NoError(t, err)
	// Order: keyless first (matches anthropic.New convention), api_key second.
	assert.Equal(t, []string{"keyless", "api_key"}, p.AuthSchemes())
}

func TestProviderModelsCatalog(t *testing.T) {
	p, err := ollama.New(ollama.WithBaseURL("http://example/v1"))
	require.NoError(t, err)
	catalog := p.Models()
	require.NotEmpty(t, catalog)
	// We always ship llama3.2 — it's our default model id.
	ids := make([]string, 0, len(catalog))
	for _, m := range catalog {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, "llama3.2")
}