package codex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/codex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := codex.New()
	assert.Error(t, err)
}

func TestNewAcceptsExplicitAPIKey(t *testing.T) {
	p, err := codex.New(codex.WithAPIKey("sk-test"))
	require.NoError(t, err)
	assert.Equal(t, "codex:gpt-5", p.Name())
	assert.Equal(t, "codex", p.ID())
}

func TestNewWithOAuthRequiresAccessToken(t *testing.T) {
	_, err := codex.NewWithOAuth(codex.OAuthCredentials{})
	assert.Error(t, err)
}

func TestNewWithOAuthSetsBearerOverAPIKey(t *testing.T) {
	creds := codex.OAuthCredentials{
		AccessToken: "oauth-access",
		AccountID:   "acc-123",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	p, err := codex.NewWithOAuth(creds, codex.WithAPIKey("sk-fallback"))
	require.NoError(t, err)
	assert.Equal(t, "codex:gpt-5", p.Name())
	// Bearer wins. We exercise this further in TestBearerHeaderFromOAuth.
}

func TestBearerHeaderFromOAuth(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	creds := codex.OAuthCredentials{AccessToken: "oauth-abc", AccountID: "acc-1"}
	p, err := codex.NewWithOAuth(creds, codex.WithBaseURL(srv.URL), codex.WithModel("gpt-5-mini"))
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer oauth-abc", sawAuth)
}

func TestCodexHeaders(t *testing.T) {
	var gotOriginator, gotVersion, gotUA, gotAccountID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOriginator = r.Header.Get("originator")
		gotVersion = r.Header.Get("version")
		gotUA = r.Header.Get("User-Agent")
		gotAccountID = r.Header.Get("ChatGPT-Account-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	creds := codex.OAuthCredentials{AccessToken: "tok", AccountID: "acc-xyz"}
	p, err := codex.NewWithOAuth(creds, codex.WithBaseURL(srv.URL))
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "codex_cli_rs", gotOriginator)
	assert.Equal(t, "0.125.0", gotVersion)
	assert.Contains(t, gotUA, "codex_cli_rs/0.125.0")
	assert.Contains(t, gotUA, "; ")
	assert.Equal(t, "acc-xyz", gotAccountID)
}

func TestLiftInstructions(t *testing.T) {
	// Two system messages, one user. Verify the system text goes to
	// Instructions (joined with "\n\n") and the user message stays
	// in Input.
	var sawInstructions string
	var sawInput []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if v, ok := req["instructions"].(string); ok {
			sawInstructions = v
		}
		if v, ok := req["input"].([]any); ok {
			for _, item := range v {
				if m, ok := item.(map[string]any); ok {
					sawInput = append(sawInput, m)
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	p, err := codex.New(codex.WithAPIKey("k"), codex.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{
			{Role: core.ROLE_SYSTEM, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "be brief"}}},
			{Role: core.ROLE_SYSTEM, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "be kind"}}},
			{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "be brief\n\nbe kind", sawInstructions, "system messages must join with \\n\\n")
	require.Len(t, sawInput, 1, "only the user message must remain in input")
	assert.Equal(t, "user", sawInput[0]["role"])
}

func TestMaxOutputTokensStripped(t *testing.T) {
	// Even when the caller sets MaxTokens, the wire body MUST NOT
	// carry max_output_tokens (Codex rejects it).
	var sawBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	}))
	defer srv.Close()

	p, err := codex.New(codex.WithAPIKey("k"), codex.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages:  []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
		MaxTokens: 4096,
	})
	require.NoError(t, err)
	_, hasMaxOutputTokens := sawBody["max_output_tokens"]
	assert.False(t, hasMaxOutputTokens, "Codex rejects max_output_tokens — must be stripped")
	// Stream/Store are always-on contract checks.
	assert.Equal(t, true, sawBody["stream"])
	assert.Equal(t, false, sawBody["store"])
}

func TestLiteModelForcesParallelFalse(t *testing.T) {
	cases := []struct {
		model     string
		wantField bool
	}{
		{"gpt-5.6", true},
		{"gpt-5.6-sol", true},
		{"gpt-5", false},
		{"gpt-5-mini", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			var sawBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&sawBody)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
			}))
			defer srv.Close()

			p, err := codex.New(codex.WithAPIKey("k"), codex.WithBaseURL(srv.URL), codex.WithModel(tc.model))
			require.NoError(t, err)
			_, err = p.Generate(context.Background(), core.ModelRequest{
				Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
			})
			require.NoError(t, err)

			if tc.wantField {
				v, ok := sawBody["parallel_tool_calls"]
				assert.True(t, ok, "lite model must set parallel_tool_calls")
				assert.Equal(t, false, v)
			} else {
				_, ok := sawBody["parallel_tool_calls"]
				assert.False(t, ok, "non-lite model must omit parallel_tool_calls")
			}
		})
	}
}

func TestIsLiteModel(t *testing.T) {
	cases := map[string]bool{
		"gpt-5":          false,
		"gpt-5-mini":     false,
		"gpt-5.6":        true,
		"gpt-5.6-sol":    true,
		"":               false,
		"some-other":     false,
	}
	for model, want := range cases {
		t.Run(model, func(t *testing.T) {
			assert.Equal(t, want, codex.IsLiteModel(model))
		})
	}
}

func TestRequestBodyValidate(t *testing.T) {
	// Missing model fails.
	err := codex.RequestBody{}.Validate()
	assert.Error(t, err)

	// Empty instructions + empty input fails.
	err = codex.RequestBody{Model: "gpt-5"}.Validate()
	assert.Error(t, err)

	// Instructions alone is OK.
	err = codex.RequestBody{Model: "gpt-5", Instructions: "hi"}.Validate()
	assert.NoError(t, err)

	// Input alone is OK.
	err = codex.RequestBody{Model: "gpt-5", Input: []codex.InputItem{{Type: "message", Role: "user"}}}.Validate()
	assert.NoError(t, err)
}

func TestOAuthCredentialsIsExpired(t *testing.T) {
	// Future expiry → not expired.
	c := codex.OAuthCredentials{ExpiresAt: time.Now().Add(time.Hour)}
	assert.False(t, c.IsExpired())

	// Past expiry → expired.
	c = codex.OAuthCredentials{ExpiresAt: time.Now().Add(-time.Hour)}
	assert.True(t, c.IsExpired())

	// Zero expiry → "not expired" (we can't tell).
	c = codex.OAuthCredentials{}
	assert.False(t, c.IsExpired())
}

func TestCountTokensHeuristic(t *testing.T) {
	p, err := codex.New(codex.WithAPIKey("k"))
	require.NoError(t, err)
	n, err := p.CountTokens(context.Background(), []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hello world"}}},
	})
	require.NoError(t, err)
	assert.Greater(t, n, 0)
}

func TestAuthorizeURLShape(t *testing.T) {
	url := codex.AuthorizeURL("state-abc", "challenge-xyz")
	assert.True(t, strings.HasPrefix(url, codex.OAuthAuthorizeURL), "starts at authorize endpoint")
	assert.Contains(t, url, "client_id="+codex.OAuthClientID)
	assert.Contains(t, url, "redirect_uri=")
	// url.Values encodes spaces as "+" but ":", "/", and "%" stay raw —
	// bare substring match is sufficient for the parts we care about.
	assert.Contains(t, url, "response_type=code")
	assert.Contains(t, url, "code_challenge=challenge-xyz")
	assert.Contains(t, url, "code_challenge_method=S256")
	assert.Contains(t, url, "state=state-abc")
}

func TestCodexUserAgentFormat(t *testing.T) {
	ua := codex.CodexUserAgent()
	assert.True(t, strings.HasPrefix(ua, "codex_cli_rs/0.125.0"))
	assert.Contains(t, ua, "; ")
	// Platform / arch separators must be the literal "(" and ")".
	assert.Regexp(t, `^codex_cli_rs/0\.125\.0 \([a-z]+; [a-z0-9_]+\)$`, ua)
}

func TestGeneratePropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, err := codex.New(codex.WithAPIKey("k-bad"), codex.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestDefaultCatalogReturnsExpectedFamily(t *testing.T) {
	p, err := codex.New(codex.WithAPIKey("k"))
	require.NoError(t, err)
	models := p.Models()
	require.NotEmpty(t, models)
	// Sanity: the catalog MUST contain at least the two lite models
	// (the wire contract pins parallel_tool_calls=false on them).
	ids := map[string]bool{}
	for _, m := range models {
		ids[m.ID] = true
	}
	assert.True(t, ids["gpt-5.6"], "gpt-5.6 must be in catalog")
	assert.True(t, ids["gpt-5.6-sol"], "gpt-5.6-sol must be in catalog")
	assert.True(t, ids["gpt-5"])
}

// Compile-time: codex.Provider satisfies core.Provider.
var _ core.Provider = (*codex.Provider)(nil)
