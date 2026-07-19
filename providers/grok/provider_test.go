package grok_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/grok"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeGrok stands up a minimal HTTP server that pretends to be the
// xAI Grok chat-completions endpoint. The handler returns a canned
// OpenAI-compat response regardless of body shape.
func newFakeGrok(t *testing.T) *httptest.Server {
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
		    {"message": {"role": "assistant", "content": "hello from grok"}, "finish_reason": "stop"}
		  ],
		  "usage": {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	_, err := grok.New()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API key")
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("XAI_API_KEY", "xai-from-env")
	p, err := grok.New()
	require.NoError(t, err)
	assert.Equal(t, "grok:grok-3", p.Name())
	assert.Equal(t, "grok", p.ID())
}

func TestGenerateAgainstFakeServer(t *testing.T) {
	srv := newFakeGrok(t)
	p, err := grok.New(
		grok.WithBaseURL(srv.URL),
		grok.WithAPIKey("xai-test"),
		grok.WithModel("grok-4"),
	)
	require.NoError(t, err)
	assert.Equal(t, "grok:grok-4", p.Name())

	req := core.ModelRequest{Messages: []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}},
	}}
	mr, err := p.Generate(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "hello from grok", mr.Text)
	assert.Equal(t, "stop", mr.StopReason)
	assert.Equal(t, 12, mr.Usage.TotalTokens)
}

func TestBearerHeaderFromAPIKey(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p, err := grok.New(grok.WithBaseURL(srv.URL), grok.WithAPIKey("xai-key"))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.True(t, strings.Contains(sawAuth, "Bearer xai-key"),
		"expected Authorization: Bearer xai-key, got %q", sawAuth)
}

func TestBearerHeaderFromOAuth(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	// OAuth path: even if an API key is configured, the bearer wins.
	creds := grok.OAuthCredentials{
		AccessToken: "oauth-access-token",
		RefreshToken: "oauth-refresh",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}
	p, err := grok.NewWithOAuth(
		creds,
		grok.WithBaseURL(srv.URL),
		grok.WithAPIKey("xai-should-be-ignored"),
	)
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.True(t, strings.Contains(sawAuth, "Bearer oauth-access-token"),
		"expected Authorization: Bearer oauth-access-token, got %q", sawAuth)
	assert.NotContains(t, sawAuth, "xai-should-be-ignored",
		"apiKey should not leak into Authorization header when OAuth bearer is set")
}

func TestNewWithOAuthRejectsEmptyToken(t *testing.T) {
	_, err := grok.NewWithOAuth(grok.OAuthCredentials{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OAuth")
}

func TestRequestBodyValidate(t *testing.T) {
	cases := []struct {
		name    string
		body    grok.RequestBody
		wantErr string
	}{
		{
			name:    "missing model",
			body:    grok.RequestBody{Messages: []grok.ChatMessage{{Role: "user", Content: "hi"}}},
			wantErr: "model is required",
		},
		{
			name:    "missing messages",
			body:    grok.RequestBody{Model: "grok-3"},
			wantErr: "at least one message",
		},
		{
			name: "bad role",
			body: grok.RequestBody{
				Model:    "grok-3",
				Messages: []grok.ChatMessage{{Role: "alien", Content: "hi"}},
			},
			wantErr: "role",
		},
		{
			name: "empty content",
			body: grok.RequestBody{
				Model:    "grok-3",
				Messages: []grok.ChatMessage{{Role: "user"}},
			},
			wantErr: "empty content",
		},
		{
			name: "happy path",
			body: grok.RequestBody{
				Model:    "grok-3",
				Messages: []grok.ChatMessage{{Role: "user", Content: "hi"}},
			},
			wantErr: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.body.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestOAuthCredentialsIsExpired(t *testing.T) {
	future := grok.OAuthCredentials{ExpiresAt: time.Now().Add(1 * time.Hour)}
	past := grok.OAuthCredentials{ExpiresAt: time.Now().Add(-1 * time.Hour)}
	zero := grok.OAuthCredentials{}

	assert.False(t, future.IsExpired(), "1h in the future should not be expired")
	assert.True(t, past.IsExpired(), "1h in the past should be expired")
	assert.False(t, zero.IsExpired(), "zero time means unknown / not expired yet")

	// 60s grace window: a token expiring in 30s is considered expired.
	withinGrace := grok.OAuthCredentials{ExpiresAt: time.Now().Add(30 * time.Second)}
	assert.True(t, withinGrace.IsExpired(), "within 60s grace window should be expired")
}

func TestProviderAuthSchemes(t *testing.T) {
	p, err := grok.New(grok.WithAPIKey("xai-test"))
	require.NoError(t, err)
	assert.Equal(t, []string{"api_key", "oauth"}, p.AuthSchemes())
}

func TestProviderModelsContainsExpectedIDs(t *testing.T) {
	p, err := grok.New(grok.WithAPIKey("xai-test"))
	require.NoError(t, err)
	models := p.Models()
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	for _, want := range []string{"grok-3", "grok-4", "grok-3-mini"} {
		assert.Contains(t, ids, want, "catalog must include %s", want)
	}
}

func TestResolveAPIKeyPrecedence(t *testing.T) {
	t.Setenv("XAI_API_KEY", "from-env")
	assert.Equal(t, "explicit", grok.ResolveAPIKey("explicit"))
	assert.Equal(t, "from-env", grok.ResolveAPIKey(""))
}

func TestResolveBaseURLPrecedence(t *testing.T) {
	t.Setenv("XAI_BASE_URL", "https://env.example/v1")
	assert.Equal(t, "https://explicit.example/v1", grok.ResolveBaseURL("https://explicit.example/v1"))
	assert.Equal(t, "https://env.example/v1", grok.ResolveBaseURL(""))

	// Clear env so the next assertion exercises the default-fallback branch.
	t.Setenv("XAI_BASE_URL", "")
	assert.Equal(t, grok.DefaultBaseURL, grok.ResolveBaseURL(""))
}

func TestStreamAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p, err := grok.New(grok.WithBaseURL(srv.URL), grok.WithAPIKey("xai-test"))
	require.NoError(t, err)

	ch, err := p.Stream(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)

	var assembled strings.Builder
	for c := range ch {
		if c.Done {
			break
		}
		assembled.WriteString(c.Text)
	}
	assert.Equal(t, "hello world", assembled.String())
}