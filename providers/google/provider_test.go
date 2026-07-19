package google_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/proxy/providers/google"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeGoogle stands up a minimal HTTP server that pretends to be
// Google Generative AI's OpenAI-compatible endpoint. The handler
// inspects the /chat/completions request body and returns a small
// canned response.
func newFakeGoogle(t *testing.T) *httptest.Server {
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
		    {"message": {"role": "assistant", "content": "hello from google"}, "finish_reason": "stop"}
		  ],
		  "usage": {"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProviderRoundTripAgainstFakeGoogle(t *testing.T) {
	srv := newFakeGoogle(t)
	p, err := google.New(google.WithBaseURL(srv.URL), google.WithAPIKey("test-key"))
	require.NoError(t, err)
	assert.Equal(t, "google:gemini-3-flash-preview", p.Name())
	assert.Equal(t, "google", p.ID())

	req := core.ModelRequest{Messages: []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}},
	}}
	mr, err := p.Generate(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "hello from google", mr.Text)
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

	p, err := google.New(google.WithBaseURL(srv.URL), google.WithAPIKey("sk-test"))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.True(t, strings.Contains(sawAuth, "Bearer sk-test"))
}

func TestProviderPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"rate-limited"}}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p, err := google.New(google.WithBaseURL(srv.URL), google.WithAPIKey("test-key"))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestProviderRejectsEmptyAPIKey(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	_, err := google.New(google.WithBaseURL("http://example/v1"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "API key")
}

func TestRequestBodyValidate(t *testing.T) {
	// empty model — must fail
	err := google.RequestBody{Messages: []google.ChatMessage{{Role: "user"}}}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model")

	// empty messages — must fail
	err = google.RequestBody{Model: "gemini-3-flash-preview"}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one message")

	// bad role — must fail
	err = google.RequestBody{
		Model:    "gemini-3-flash-preview",
		Messages: []google.ChatMessage{{Role: "bogus"}},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "role")

	// happy path — all four roles pass
	for _, role := range []string{"system", "user", "assistant", "tool"} {
		err = google.RequestBody{
			Model:    "gemini-3-flash-preview",
			Messages: []google.ChatMessage{{Role: role}},
		}.Validate()
		assert.NoError(t, err, "role %q should validate", role)
	}
}

func TestAuthResolvers(t *testing.T) {
	t.Run("ResolveAPIKey prefers explicit over env", func(t *testing.T) {
		t.Setenv("GOOGLE_API_KEY", "env-key")
		assert.Equal(t, "explicit-key", google.ResolveAPIKey("explicit-key"))
	})

	t.Run("ResolveAPIKey falls back to env", func(t *testing.T) {
		t.Setenv("GOOGLE_API_KEY", "env-key")
		assert.Equal(t, "env-key", google.ResolveAPIKey(""))
	})

	t.Run("ResolveAPIKey empty when both are empty", func(t *testing.T) {
		t.Setenv("GOOGLE_API_KEY", "")
		assert.Equal(t, "", google.ResolveAPIKey(""))
	})

	t.Run("ResolveBaseURL prefers explicit over env", func(t *testing.T) {
		t.Setenv("GOOGLE_BASE_URL", "https://env-host/v1beta/openai")
		assert.Equal(t, "https://explicit/v1beta/openai", google.ResolveBaseURL("https://explicit/v1beta/openai"))
	})

	t.Run("ResolveBaseURL falls back to env", func(t *testing.T) {
		t.Setenv("GOOGLE_BASE_URL", "https://env-host/v1beta/openai")
		assert.Equal(t, "https://env-host/v1beta/openai", google.ResolveBaseURL(""))
	})

	t.Run("ResolveBaseURL defaults to AI Studio", func(t *testing.T) {
		t.Setenv("GOOGLE_BASE_URL", "")
		assert.Equal(t, "https://generativelanguage.googleapis.com/v1beta/openai", google.ResolveBaseURL(""))
	})
}

func TestProviderAuthSchemes(t *testing.T) {
	p, err := google.New(google.WithBaseURL("http://example/v1beta/openai"), google.WithAPIKey("test-key"))
	require.NoError(t, err)
	assert.Equal(t, []string{"api_key"}, p.AuthSchemes())
}

func TestProviderModelsCatalog(t *testing.T) {
	p, err := google.New(google.WithBaseURL("http://example/v1beta/openai"), google.WithAPIKey("test-key"))
	require.NoError(t, err)
	catalog := p.Models()
	require.NotEmpty(t, catalog)
	ids := make([]string, 0, len(catalog))
	for _, m := range catalog {
		ids = append(ids, m.ID)
	}
	// All 10 catalogued models must be present.
	want := []string{
		"gemma-4-31b-it",
		"gemma-4-26b-a4b-it",
		"gemini-embedding-2",
		"gemini-embedding-2-preview",
		"gemini-3.1-flash-lite",
		"gemini-embedding-001",
		"gemini-3.5-flash",
		"imagen-4.0-fast-generate-001",
		"imagen-4.0-generate-001",
		"imagen-4.0-ultra-generate-001",
		"gemini-3-flash-preview",
	}
	for _, id := range want {
		assert.Contains(t, ids, id)
	}
}

func TestProviderStreamAgainstFakeGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p, err := google.New(google.WithBaseURL(srv.URL), google.WithAPIKey("test-key"))
	require.NoError(t, err)

	ch, err := p.Stream(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)

	var got strings.Builder
	done := false
	for c := range ch {
		if c.Done {
			done = true
			continue
		}
		if c.Kind == core.PART_KIND_PLAIN_TEXT {
			got.WriteString(c.Text)
		}
	}
	assert.True(t, done, "stream should end with Done chunk")
	assert.Equal(t, "hello world", got.String())
}
