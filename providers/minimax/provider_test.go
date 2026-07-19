package minimax_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/minimax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cannedAnthropicResponse is the minimal /v1/messages non-stream response
// minimax returns. Wire format matches Anthropic's spec.
const cannedAnthropicResponse = `{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "model": "minimax-M2",
  "stop_reason": "end_turn",
  "content": [
    {"type": "text", "text": "hello from minimax"}
  ],
  "usage": {"input_tokens": 7, "output_tokens": 4}
}`

// newFakeMinimax stands up an httptest server that pretends to be the
// minimax Anthropic-compat endpoint. The handler records the inbound
// request headers and body for assertion, and returns cannedAnthropicResponse.
func newFakeMinimax(t *testing.T) (*httptest.Server, *http.Request, *string) {
	t.Helper()
	var seenBody string
	var seenReq http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenReq = *r
		body, _ := io.ReadAll(r.Body)
		seenBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(cannedAnthropicResponse))
	}))
	t.Cleanup(srv.Close)
	return srv, &seenReq, &seenBody
}

func TestNewRequiresAPIKey(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "")
	_, err := minimax.New()
	assert.Error(t, err)
}

func TestResolveBaseURLDefault(t *testing.T) {
	assert.Equal(t, "https://api.minimax.io/anthropic", minimax.ResolveBaseURL(""))
}

func TestResolveBaseURLExplicit(t *testing.T) {
	assert.Equal(t, "https://example.com/proxy", minimax.ResolveBaseURL("https://example.com/proxy"))
}

func TestResolveBaseURLEnvOverride(t *testing.T) {
	t.Setenv("MINIMAX_BASE_URL", "https://env.example.com/anthropic")
	assert.Equal(t, "https://env.example.com/anthropic", minimax.ResolveBaseURL(""))
}

func TestResolveAPIKeyExplicitWins(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "from-env")
	assert.Equal(t, "from-explicit", minimax.ResolveAPIKey("from-explicit"))
}

func TestResolveAPIKeyEnvFallback(t *testing.T) {
	t.Setenv("MINIMAX_API_KEY", "from-env")
	assert.Equal(t, "from-env", minimax.ResolveAPIKey(""))
}

func TestBearerHeader(t *testing.T) {
	// minimax uses x-api-key, NOT Authorization: Bearer — that's the
	// Anthropic-Messages convention.
	srv, seen, _ := newFakeMinimax(t)
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test-key"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)

	assert.Equal(t, "sk-test-key", seen.Header.Get("x-api-key"))
	assert.Empty(t, seen.Header.Get("Authorization"))
}

func TestGenerateAgainstFakeServer(t *testing.T) {
	srv, _, _ := newFakeMinimax(t)
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	mr, err := p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)
	assert.Equal(t, "hello from minimax", mr.Text)
	assert.Equal(t, "end_turn", mr.StopReason)
	assert.Equal(t, 7, mr.Usage.PromptTokens)
	assert.Equal(t, 4, mr.Usage.CompletionTokens)
	assert.Equal(t, 11, mr.Usage.TotalTokens)
}

func TestRequestBodyValidate(t *testing.T) {
	// Missing model.
	err := minimax.RequestBody{}.Validate()
	assert.Error(t, err)

	// Missing max_tokens.
	err = minimax.RequestBody{Model: "minimax-M2"}.Validate()
	assert.Error(t, err)

	// Missing messages.
	err = minimax.RequestBody{Model: "minimax-M2", MaxTokens: 100}.Validate()
	assert.Error(t, err)

	// Bad role.
	err = minimax.RequestBody{
		Model:     "minimax-M2",
		MaxTokens: 100,
		Messages:  []minimax.MessageParam{{Role: "tool", Content: []minimax.ContentParam{{Type: "text", Text: "x"}}}},
	}.Validate()
	assert.Error(t, err)

	// Empty content.
	err = minimax.RequestBody{
		Model:     "minimax-M2",
		MaxTokens: 100,
		Messages:  []minimax.MessageParam{{Role: "user", Content: nil}},
	}.Validate()
	assert.Error(t, err)

	// Happy path.
	err = minimax.RequestBody{
		Model:     "minimax-M2",
		MaxTokens: 100,
		Messages:  []minimax.MessageParam{{Role: "user", Content: []minimax.ContentParam{{Type: "text", Text: "hi"}}}},
	}.Validate()
	assert.NoError(t, err)
}

func TestSystemPromptLifted(t *testing.T) {
	// System messages must end up in the top-level `system` field, not
	// inside the messages array.
	srv, _, seenBody := newFakeMinimax(t)
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{
			{Role: core.ROLE_SYSTEM, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "be terse"}}},
			{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "ping"}}},
		},
	})
	require.NoError(t, err)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(*seenBody), &sent))
	assert.Equal(t, "be terse", sent["system"])

	msgs, ok := sent["messages"].([]any)
	require.True(t, ok)
	for _, raw := range msgs {
		m := raw.(map[string]any)
		assert.NotEqual(t, "system", m["role"], "system role must not appear inside messages")
	}
}

func TestMaxTokensDefault(t *testing.T) {
	// When req.MaxTokens is 0, we default to 4096 so the API never rejects
	// us with "max_tokens must be specified".
	srv, _, seenBody := newFakeMinimax(t)
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(*seenBody), &sent))
	// JSON numbers decode to float64.
	assert.Equal(t, float64(4096), sent["max_tokens"])
}

func TestMaxTokensExplicit(t *testing.T) {
	// When req.MaxTokens is set, we forward it verbatim.
	srv, _, seenBody := newFakeMinimax(t)
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	_, err = p.Generate(context.Background(), core.ModelRequest{
		MaxTokens: 1024,
		Messages:  []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(*seenBody), &sent))
	assert.Equal(t, float64(1024), sent["max_tokens"])
}

func TestProviderIDAndModels(t *testing.T) {
	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL("https://api.minimax.io/anthropic"),
	)
	require.NoError(t, err)
	assert.Equal(t, "minimax", p.ID())
	assert.Equal(t, "minimax:minimax-M2", p.Name())
	assert.Equal(t, []string{"api_key"}, p.AuthSchemes())
	assert.NotEmpty(t, p.Models())
}

func TestStreamAgainstFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	p, err := minimax.New(
		minimax.WithAPIKey("sk-test"),
		minimax.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	ch, err := p.Stream(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
	})
	require.NoError(t, err)

	var saw string
	var done bool
	for c := range ch {
		if c.Done {
			done = true
			continue
		}
		saw += c.Text
	}
	assert.True(t, done)
	assert.True(t, strings.Contains(saw, "hi"))
}

func TestCountTokensHeuristic(t *testing.T) {
	p, err := minimax.New(minimax.WithAPIKey("sk-test"))
	require.NoError(t, err)
	n, err := p.CountTokens(context.Background(), []core.Message{
		{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hello world"}}},
	})
	require.NoError(t, err)
	assert.Greater(t, n, 0)
}
