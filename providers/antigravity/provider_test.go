package antigravity_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/antigravity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewRequiresAPIKey — without an explicit key or env, New() fails fast.
func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("ANTIGRAVITY_API_KEY", "")
	_, err := antigravity.New()
	assert.Error(t, err)
}

// TestNewWithExplicitAPIKey — explicit option wins.
func TestNewWithExplicitAPIKey(t *testing.T) {
	t.Setenv("ANTIGRAVITY_API_KEY", "")
	p, err := antigravity.New(antigravity.WithAPIKey("sk-direct"))
	require.NoError(t, err)
	assert.Equal(t, "antigravity:claude-sonnet-5", p.Name())
	assert.Equal(t, "antigravity", p.ID())
}

// TestGenerateAgainstFakeServer — spin up an httptest server that mimics
// the Anthropic-Messages response shape and verify Generate() round-trips.
func TestGenerateAgainstFakeServer(t *testing.T) {
	var (
		gotAuth string
		gotPath string
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-5",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "hello back"}],
			"usage": {"input_tokens": 7, "output_tokens": 3}
		}`))
	}))
	defer srv.Close()

	t.Setenv("ANTIGRAVITY_API_KEY", "sk-from-env")
	t.Setenv("ANTIGRAVITY_BASE_URL", srv.URL)

	p, err := antigravity.New()
	require.NoError(t, err)

	res, err := p.Generate(context.Background(), core.ModelRequest{
		MaxTokens: 128,
		Messages: []core.Message{
			{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hello"}}},
		},
	})
	require.NoError(t, err)
	// baseURL is the test server (no /v1), so the path is /messages.
	// The default gateway URL embeds /v1, so production sends /v1/messages.
	assert.Equal(t, "/messages", gotPath)
	assert.Equal(t, "hello back", res.Text)
	assert.Equal(t, "end_turn", res.StopReason)
	assert.Equal(t, 7, res.Usage.PromptTokens)
	assert.Equal(t, 3, res.Usage.CompletionTokens)
	assert.Equal(t, 10, res.Usage.TotalTokens)

	// Body sanity: model + max_tokens + a message made it across.
	var sent map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &sent))
	assert.Equal(t, "claude-sonnet-5", sent["model"])
	assert.Equal(t, float64(128), sent["max_tokens"])
	msgs, ok := sent["messages"].([]any)
	require.True(t, ok)
	assert.NotEmpty(t, msgs)

	// Default auth is the API-key path.
	assert.Empty(t, gotAuth, "API-key mode must not send an Authorization header")
}

// TestBearerHeaderFromOAuth — when constructed via NewWithOAuth the request
// carries Authorization: Bearer <token>, not x-api-key.
func TestBearerHeaderFromOAuth(t *testing.T) {
	var gotAuth, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_oauth",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-5",
			"stop_reason": "end_turn",
			"content": [{"type": "text", "text": "ok"}],
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`))
	}))
	defer srv.Close()

	t.Setenv("ANTIGRAVITY_API_KEY", "")
	t.Setenv("ANTIGRAVITY_BASE_URL", srv.URL)

	creds := antigravity.OAuthCredentials{
		AccessToken: "ya29.fake-bearer-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	p, err := antigravity.NewWithOAuth(creds)
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		MaxTokens: 64,
		Messages: []core.Message{
			{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "ping"}}},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer ya29.fake-bearer-token", gotAuth)
	assert.Empty(t, gotAPIKey, "OAuth mode must not send x-api-key")
}

// TestNewWithOAuthRejectsEmptyToken — defensive check.
func TestNewWithOAuthRejectsEmptyToken(t *testing.T) {
	t.Setenv("ANTIGRAVITY_API_KEY", "")
	_, err := antigravity.NewWithOAuth(antigravity.OAuthCredentials{})
	assert.Error(t, err)
}

// TestRequestBodyValidate — covers the four Validate() failure modes.
func TestRequestBodyValidate(t *testing.T) {
	cases := []struct {
		name string
		body antigravity.RequestBody
		want string
	}{
		{
			name: "empty model",
			body: antigravity.RequestBody{MaxTokens: 1, Messages: []antigravity.MessageParam{
				{Role: "user", Content: []antigravity.ContentParam{{Type: "text", Text: "hi"}}},
			}},
			want: "model is required",
		},
		{
			name: "zero max_tokens",
			body: antigravity.RequestBody{Model: "claude-sonnet-5", Messages: []antigravity.MessageParam{
				{Role: "user", Content: []antigravity.ContentParam{{Type: "text", Text: "hi"}}},
			}},
			want: "max_tokens must be positive",
		},
		{
			name: "no messages",
			body: antigravity.RequestBody{Model: "claude-sonnet-5", MaxTokens: 1},
			want: "at least one message",
		},
		{
			name: "bad role",
			body: antigravity.RequestBody{
				Model: "claude-sonnet-5", MaxTokens: 1,
				Messages: []antigravity.MessageParam{{Role: "system", Content: []antigravity.ContentParam{{Type: "text", Text: "x"}}}},
			},
			want: "role",
		},
		{
			name: "empty content",
			body: antigravity.RequestBody{
				Model: "claude-sonnet-5", MaxTokens: 1,
				Messages: []antigravity.MessageParam{{Role: "user"}},
			},
			want: "no content blocks",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.body.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestOAuthCredentialsIsExpired — confirms the 60s grace window + zero
// ExpiresAt behavior.
func TestOAuthCredentialsIsExpired(t *testing.T) {
	past := antigravity.OAuthCredentials{ExpiresAt: time.Now().Add(-time.Hour)}
	future := antigravity.OAuthCredentials{ExpiresAt: time.Now().Add(time.Hour)}
	withinGrace := antigravity.OAuthCredentials{ExpiresAt: time.Now().Add(30 * time.Second)}
	zero := antigravity.OAuthCredentials{}

	assert.True(t, past.IsExpired(), "past expiry must report expired")
	assert.False(t, future.IsExpired(), "future expiry must report fresh")
	assert.True(t, withinGrace.IsExpired(), "within 60s grace window must report expired")
	assert.False(t, zero.IsExpired(), "zero ExpiresAt must NOT report expired")
}

// TestGeneratePKCEReturnsVerifierAndChallenge — verifier/challenge are
// non-empty and the challenge is the S256 hash of the verifier.
func TestGeneratePKCEReturnsVerifierAndChallenge(t *testing.T) {
	verifier, challenge, err := antigravity.GeneratePKCE()
	require.NoError(t, err)
	assert.NotEmpty(t, verifier)
	assert.NotEmpty(t, challenge)
	assert.NotEqual(t, verifier, challenge)

	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.Equal(t, want, challenge)
}

// TestAuthorizeURLShape — ensures the URL carries the params Google expects.
func TestAuthorizeURLShape(t *testing.T) {
	u := antigravity.AuthorizeURL("opaque-state", "challenge-value")
	assert.Contains(t, u, "accounts.google.com/o/oauth2/v2/auth")
	assert.Contains(t, u, "client_id=antigravity-cli")
	assert.Contains(t, u, "response_type=code")
	assert.Contains(t, u, "state=opaque-state")
	assert.Contains(t, u, "code_challenge=challenge-value")
	assert.Contains(t, u, "code_challenge_method=S256")
	assert.Contains(t, u, "access_type=offline")
	assert.Contains(t, u, "prompt=consent")
}