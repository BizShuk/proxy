package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterResponseHeaders_RemovesSensitiveKeys(t *testing.T) {
	in := http.Header{}
	in.Set("Authorization", "Bearer secret")
	in.Set("Set-Cookie", "sid=abc; HttpOnly")
	in.Set("X-API-Key", "sk-123")
	in.Set("Retry-After", "30")
	in.Set("X-Request-ID", "req-1")
	in.Add("X-Custom", "v1")
	in.Add("X-Custom", "v2")

	out := filterResponseHeaders(in)

	assert.Equal(t, "30", out.Get("Retry-After"))
	assert.Equal(t, "req-1", out.Get("X-Request-ID"))
	assert.Equal(t, []string{"v1", "v2"}, out.Values("X-Custom"))
	assert.Empty(t, out.Get("Authorization"))
	assert.Empty(t, out.Get("Set-Cookie"))
	assert.Empty(t, out.Get("X-API-Key"))
}

func TestFilterResponseHeaders_NilInputReturnsEmpty(t *testing.T) {
	out := filterResponseHeaders(nil)
	assert.NotNil(t, out)
	assert.Empty(t, out)
}

// captureLogs swaps slog.Default() to a JSON-handler-backed
// buffer for the duration of the test, restoring the prior
// default on cleanup. Pattern mirrors codex_log_test.go.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// decodeLastLog decodes the most recent JSON log line.
func decodeLastLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.NotEmpty(t, lines, "no log lines captured")
	last := lines[len(lines)-1]
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(last), &out))
	return out
}

func newResponse(status int, header http.Header, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestLogUpstreamError_IncludesAllFields(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	resp := newResponse(500, http.Header{
		"Retry-After":  []string{"30"},
		"X-Request-Id": []string{"upstream-req-42"},
	}, `{"err":"boom"}`)

	body := h.logUpstreamError(context.Background(), "req-1", "gpt-5", "openai", resp)

	require.NotNil(t, body)
	assert.Equal(t, `{"err":"boom"}`, string(body))
	entry := decodeLastLog(t, buf)
	assert.Equal(t, "proxy upstream error response", entry["msg"])
	assert.Equal(t, "ERROR", entry["level"])
	assert.Equal(t, "req-1", entry["request_id"])
	assert.Equal(t, "openai", entry["provider"])
	assert.Equal(t, "gpt-5", entry["model"])
	assert.Equal(t, float64(500), entry["status_code"])
	assert.Equal(t, "30", entry["header.retry-after"])
	assert.Equal(t, "upstream-req-42", entry["header.x-request-id"])
	assert.Equal(t, `{"err":"boom"}`, entry["body"])
	assert.Equal(t, false, entry["body_truncated"])
}

func TestLogUpstreamError_FiltersSensitiveHeaders(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	resp := newResponse(401, http.Header{
		"Authorization": []string{"Bearer leak"},
		"Set-Cookie":    []string{"sid=abc"},
		"X-API-Key":     []string{"sk-123"},
		"Content-Type":  []string{"application/json"},
	}, `{"err":"unauthorized"}`)

	_ = h.logUpstreamError(context.Background(), "req-2", "claude-3", "anthropic", resp)

	entry := decodeLastLog(t, buf)
	for _, forbidden := range []string{"header.authorization", "header.set-cookie", "header.x-api-key"} {
		_, present := entry[forbidden]
		assert.False(t, present, "sensitive header %q must not appear in log", forbidden)
	}
	assert.Equal(t, "application/json", entry["header.content-type"])
}

func TestLogUpstreamError_TruncatesBody(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	big := strings.Repeat("a", int(MAX_UPSTREAM_ERROR_BYTES)+1024)
	resp := newResponse(502, http.Header{}, big)

	body := h.logUpstreamError(context.Background(), "req-3", "gpt-5", "openai", resp)

	require.NotNil(t, body)
	// Returned body is uncapped; the caller (handleUpstreamError
	// → readBounded) is responsible for rejecting oversized
	// bodies. The log entry still records body_truncated=true.
	assert.Equal(t, len(big), len(body))
	entry := decodeLastLog(t, buf)
	assert.Equal(t, true, entry["body_truncated"])
	assert.Equal(t, float64(len(big)), entry["body_bytes"])
}

func TestLogUpstreamError_NilBodyNoCrash(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	resp := &http.Response{StatusCode: 503, Header: http.Header{}, Body: nil}

	body := h.logUpstreamError(context.Background(), "req-4", "gpt-5", "openai", resp)

	assert.Nil(t, body)
	entry := decodeLastLog(t, buf)
	assert.Equal(t, float64(503), entry["status_code"])
	assert.Equal(t, "response body nil", entry["body_read_error"])
	assert.Equal(t, float64(0), entry["body_bytes"])
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestLogUpstreamError_BodyReadError(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	resp := &http.Response{
		StatusCode: 502,
		Header:     http.Header{},
		Body:       io.NopCloser(failingReader{}),
	}

	body := h.logUpstreamError(context.Background(), "req-5", "gpt-5", "openai", resp)

	assert.Nil(t, body)
	entry := decodeLastLog(t, buf)
	assert.Equal(t, "proxy upstream error response", entry["msg"])
	assert.NotEmpty(t, entry["body_read_error"])
}

func TestLogStreamError_MissingResponse(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}

	h.logStreamError(context.Background(), "req-6", "gpt-5", "openai", nil, "sse_decode_error")

	entry := decodeLastLog(t, buf)
	assert.Equal(t, "proxy upstream stream error", entry["msg"])
	assert.Equal(t, "ERROR", entry["level"])
	assert.Equal(t, "req-6", entry["request_id"])
	assert.Equal(t, "openai", entry["provider"])
	assert.Equal(t, "gpt-5", entry["model"])
	assert.Equal(t, "sse_decode_error", entry["cause"])
	for k := range entry {
		assert.False(t, strings.HasPrefix(k, "header."), "no header.* attrs when response is nil; got key %q", k)
	}
}

func TestLogStreamError_IncludesFrameData(t *testing.T) {
	buf := captureLogs(t)
	h := &Handler{}
	resp := newResponse(502, http.Header{
		"X-Request-Id": []string{"upstream-req-99"},
	}, "")

	h.logStreamError(context.Background(), "req-7", "gpt-5", "openai", resp, "stream_push_error")

	entry := decodeLastLog(t, buf)
	assert.Equal(t, "stream_push_error", entry["cause"])
	assert.Equal(t, "upstream-req-99", entry["header.x-request-id"])
}
