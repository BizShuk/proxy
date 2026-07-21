package handlers

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleUpstreamError_EmitsRespBeforeSnapshot verifies that the
// 4xx/5xx path captures the upstream body in a DEBUG-level
// "proxy debug payload" record with stage=resp.before, alongside
// the existing WARN-level logUpstreamError emission. This is the
// error-path counterpart to the success-path snapshots already
// emitted by handleNonStream.
func TestHandleUpstreamError_EmitsRespBeforeSnapshot(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	gin.SetMode(gin.TestMode)

	body := []byte(`{"error":{"message":"context_length_exceeded","code":"invalid_request"}}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	h := &Handler{}
	h.handleUpstreamError(
		c,
		model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
		"req-up-1", "gpt-5.6-sol", "openai-codex-oauth",
		resp,
	)

	out := buf.String()
	require.NotEmpty(t, out, "expected at least one Debug record")
	assert.Contains(t, out, `"stage":"resp.before"`)
	assert.Contains(t, out, `"request_id":"req-up-1"`)
	assert.Contains(t, out, `"model":"gpt-5.6-sol"`)
	assert.Contains(t, out, `"provider":"openai-codex-oauth"`)
	assert.Contains(t, out, `"source_format":"anthropic-messages"`)
	assert.Contains(t, out, `"target_format":"openai-responses"`)
	// body_bytes should equal the upstream body length.
	assert.Contains(t, out, `"body_bytes":`+itoa(len(body)))
	// The actual error message must round-trip (DEBUG carries raw body).
	assert.Contains(t, out, `context_length_exceeded`)
}

// TestHandleUpstreamError_TooLargeBodyEmitsSummary covers the case
// where the upstream error body itself exceeds MAX_UPSTREAM_ERROR_BYTES.
// The error path still emits a Debug record — with an empty body and
// body_truncated=true — so operators can correlate the failure with
// the limit instead of getting a silent gap in the snapshot chain.
func TestHandleUpstreamError_TooLargeBodyEmitsSummary(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	gin.SetMode(gin.TestMode)

	// Body strictly larger than MAX_UPSTREAM_ERROR_BYTES (64 KiB).
	body := bytes.Repeat([]byte("X"), int(MAX_UPSTREAM_ERROR_BYTES)+1024)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}

	h := &Handler{}
	h.handleUpstreamError(
		c,
		model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
		"req-up-2", "gpt-5.6-sol", "openai-codex-oauth",
		resp,
	)

	out := buf.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, `"stage":"resp.before"`)
	assert.Contains(t, out, `"request_id":"req-up-2"`)
	// body itself is empty; body_truncated=true marks the overflow.
	assert.Contains(t, out, `"body_truncated":true`)
	assert.Contains(t, out, `"body":"`)
}