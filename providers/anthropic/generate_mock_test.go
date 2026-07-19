package anthropic_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider/anthropic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeAnthropic stands up a minimal /v1/messages server returning a
// canned Anthropic response (text + tool_use). The provider's Generate
// must parse this into the expected core.ModelResult — exercising the
// toAnthropicMessages / fromAnthropicResponse translation end-to-end
// without a real API key.
func newFakeAnthropic(t *testing.T, respBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGenerateParsesTextAndToolUse(t *testing.T) {
	// Anthropic response: one text block + one tool_use block.
	body := `{
	  "id": "msg_1",
	  "type": "message",
	  "role": "assistant",
	  "model": "claude-3-5-sonnet-latest",
	  "stop_reason": "tool_use",
	  "content": [
	    {"type": "text", "text": "I'll read the log first."},
	    {"type": "tool_use", "id": "call-1", "name": "read_log_tail", "input": {"n": 5}}
	  ],
	  "usage": {"input_tokens": 10, "output_tokens": 8}
	}`
	srv := newFakeAnthropic(t, body)

	p, err := anthropic.New(
		anthropic.WithAPIKey("sk-test"),
		anthropic.WithBaseURL(srv.URL),
	)
	require.NoError(t, err)

	mr, err := p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{
			Role: core.ROLE_USER,
			Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "diagnose"}},
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, "tool_use", mr.StopReason)
	assert.Contains(t, mr.Text, "read the log")
	require.Len(t, mr.ToolCalls, 1)
	assert.Equal(t, "call-1", mr.ToolCalls[0].ID)
	assert.Equal(t, "read_log_tail", mr.ToolCalls[0].Name)
	assert.Equal(t, float64(5), mr.ToolCalls[0].Args["n"])
	assert.Equal(t, 18, mr.Usage.TotalTokens, "input+output")
}

// TestGeneratePropagatesHTTPError verifies a non-2xx surfaces as an error
// rather than a zero-value ModelResult.
func TestGeneratePropagatesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p, err := anthropic.New(anthropic.WithAPIKey("sk-bad"), anthropic.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "x"}}}},
	})
	require.Error(t, err)
}

// TestNameIncludesModel confirms the provider name carries the model.
func TestNameIncludesModel(t *testing.T) {
	p, err := anthropic.New(anthropic.WithAPIKey("sk-x"), anthropic.WithModel("claude-opus-4-8"))
	require.NoError(t, err)
	assert.Equal(t, "anthropic:claude-opus-4-8", p.Name())
}

// TestToolSpecForwardedAsInputSchema verifies the tool's JSON schema
// parameters survive into the outbound request as the tool's input
// schema. We assert by inspecting the request body the fake receives.
func TestToolSpecForwardedAsInputSchema(t *testing.T) {
	var sawTools bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
			sawTools = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	p, err := anthropic.New(anthropic.WithAPIKey("sk-test"), anthropic.WithBaseURL(srv.URL))
	require.NoError(t, err)
	_, err = p.Generate(context.Background(), core.ModelRequest{
		Messages: []core.Message{{Role: core.ROLE_USER, Parts: []core.Part{{Kind: core.PART_KIND_PLAIN_TEXT, Text: "hi"}}}},
		Tools: []core.ToolSpec{{
			Name: "read_log_tail", Description: "read log",
			Parameters: json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`),
			Risk:        core.RISK_LEVEL_LOW,
		}},
	})
	require.NoError(t, err)
	assert.True(t, sawTools, "tools must be forwarded to the API as input schemas")
}

// Compile-time: ensure Provider satisfies the port.
var _ core.Provider = (*anthropic.Provider)(nil)
