package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func antigravityCred(baseURL string) *authmodel.Credential {
	return &authmodel.Credential{
		Provider: "antigravity", Kind: authmodel.KIND_OAUTH,
		Account: "dev@example.com", AccessToken: "test-access-token",
		RefreshToken: "test-refresh-token", ExpiresAt: time.Now().Add(time.Hour),
		BaseURL: baseURL, ProjectID: "projects/demo",
	}
}

// End-to-end proof that an Anthropic Messages caller reaches the Antigravity
// gateway: routing picks the profile, the transform emits the Cloud Code
// envelope, and the Gemini response comes back as an Anthropic message.
func TestHandlerRoutesAnthropicToAntigravity(t *testing.T) {
	var gotPath, gotQuery string
	var gotBody []byte
	var gotHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"response":{
			"responseId":"resp_e2e",
			"modelVersion":"gemini-3.1-pro-high",
			"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6}
		}}`)
	}))
	defer server.Close()

	handler := newHandlerForCredential(t, antigravityCred(server.URL), server.Client())
	router := gin.New()
	router.POST("/v1/messages", handler.Handle(model.FORMAT_ANTHROPIC_MESSAGES))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(
		requestBody(model.FORMAT_ANTHROPIC_MESSAGES, "antigravity/gemini-3.1-pro-high", false))))

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "/v1internal:generateContent", gotPath)
	assert.Empty(t, gotQuery)
	assert.Equal(t, "Bearer test-access-token", gotHeader.Get("Authorization"))
	assert.Equal(t, "antigravity", gotHeader.Get("X-Client-Name"))
	assert.NotEmpty(t, gotHeader.Get("x-goog-api-client"))

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &envelope))
	assert.Equal(t, "gemini-3.1-pro-high", envelope["model"])
	assert.Equal(t, "projects/demo", envelope["project"])
	assert.Equal(t, "antigravity", envelope["userAgent"])
	assert.Equal(t, "agent", envelope["requestType"])
	assert.Contains(t, envelope, "request")

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	assert.Equal(t, "resp_e2e", decoded["id"])
	assert.Equal(t, "end_turn", decoded["stop_reason"])
}

// Streaming must hit the separate SSE path; reusing generateContent would give
// the caller a single JSON blob instead of Anthropic stream events.
func TestHandlerStreamsAnthropicFromAntigravity(t *testing.T) {
	var gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"response\":{\"responseId\":\"resp_stream\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"done\"}]}}]}}\n\n")
		_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":1}}}\n\n")
	}))
	defer server.Close()

	handler := newHandlerForCredential(t, antigravityCred(server.URL), server.Client())
	router := gin.New()
	router.POST("/v1/messages", handler.Handle(model.FORMAT_ANTHROPIC_MESSAGES))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(
		requestBody(model.FORMAT_ANTHROPIC_MESSAGES, "antigravity/gemini-3.1-pro-high", true))))

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "/v1internal:streamGenerateContent", gotPath)
	assert.Equal(t, "alt=sse", gotQuery)

	body := response.Body.String()
	for _, event := range []string{
		"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop",
	} {
		assert.Containsf(t, body, "event: "+event, "missing %s", event)
	}
	assert.True(t, strings.Contains(body, `"stop_reason":"end_turn"`))
}
