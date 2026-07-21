package handlers

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSlogTee swaps slog.Default to a JSON handler at the supplied
// level writing into a buffer; the cleanup restores the prior logger
// so other tests aren't affected.
func withSlogTee(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestEmitDebugPayload_FilteredBySlogLevel confirms the helper
// emits unconditionally and lets the slog handler's level decide.
// At Info level the Debug record is suppressed; at Debug level it
// reaches the writer. This is the contract after we removed the
// cfg.Debug gate: only LOG_LEVEL controls emission.
func TestEmitDebugPayload_FilteredBySlogLevel(t *testing.T) {
	t.Run("info_level_suppresses_debug", func(t *testing.T) {
		buf := withSlogTee(t, slog.LevelInfo)
		h := &Handler{}
		h.emitDebugPayload(context.Background(), debugStageRequestBefore,
			"req-1", "gpt-5", "openai",
			model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
			[]byte(`{"hello":"world"}`))
		assert.Empty(t, buf.String(), "Info-level handler must drop Debug records")
	})
	t.Run("debug_level_emits", func(t *testing.T) {
		buf := withSlogTee(t, slog.LevelDebug)
		h := &Handler{}
		h.emitDebugPayload(context.Background(), debugStageRequestBefore,
			"req-1", "gpt-5", "openai",
			model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
			[]byte(`{"hello":"world"}`))
		require.NotEmpty(t, buf.String())
		assert.Contains(t, buf.String(), `"msg":"proxy debug payload"`)
	})
}

// TestEmitDebugPayload_StagesCoverAllFour asserts each stage label
// round-trips verbatim through slog. Operators rely on a fixed
// vocabulary to build log scrapers.
func TestEmitDebugPayload_StagesCoverAllFour(t *testing.T) {
	stages := []string{
		debugStageRequestBefore,
		debugStageRequestNow,
		debugStageResponseBefore,
		debugStageResponseNow,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			buf := withSlogTee(t, slog.LevelDebug)
			h := &Handler{}
			h.emitDebugPayload(context.Background(), stage,
				"req-1", "gpt-5", "openai-codex-oauth",
				model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
				[]byte(`{"k":"v"}`))
			out := buf.String()
			require.NotEmpty(t, out)
			assert.Contains(t, out, `"stage":"`+stage+`"`)
			assert.Contains(t, out, `"request_id":"req-1"`)
			assert.Contains(t, out, `"model":"gpt-5"`)
			assert.Contains(t, out, `"provider":"openai-codex-oauth"`)
			assert.Contains(t, out, `"source_format":"anthropic-messages"`)
			assert.Contains(t, out, `"target_format":"openai-responses"`)
			assert.Contains(t, out, `"body":"{\"k\":\"v\"}"`)
			assert.Contains(t, out, `"body_bytes":9`)
			assert.Contains(t, out, `"body_truncated":false`)
		})
	}
}

// TestEmitDebugPayload_BodyTruncatedAtCap confirms bodies longer
// than DEBUG_PAYLOAD_MAX_BYTES are sliced and flagged.
func TestEmitDebugPayload_BodyTruncatedAtCap(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	h := &Handler{}
	body := bytes.Repeat([]byte("X"), DEBUG_PAYLOAD_MAX_BYTES+1024)
	h.emitDebugPayload(context.Background(), debugStageResponseNow,
		"req-2", "gpt-5", "openai-codex-oauth",
		model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES, body)

	out := buf.String()
	assert.Contains(t, out, `"body_truncated":true`)
	// body_bytes must report the *original* length, not the capped one.
	expectedBytes := DEBUG_PAYLOAD_MAX_BYTES + 1024
	assert.Contains(t, out, `"body_bytes":`+itoa(expectedBytes))
	// The truncated body is exactly DEBUG_PAYLOAD_MAX_BYTES X's; sample
	// a slice that crosses the boundary to assert no leakage.
	assert.Contains(t, out, strings.Repeat("X", 32))
	assert.NotContains(t, out, strings.Repeat("X", DEBUG_PAYLOAD_MAX_BYTES+1),
		"body must be capped at DEBUG_PAYLOAD_MAX_BYTES")
}

// TestEmitDebugPayload_NilHandlerSafe guards against accidental
// calls from nil-receiver code paths.
func TestEmitDebugPayload_NilHandlerSafe(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	var h *Handler
	h.emitDebugPayload(context.Background(), debugStageRequestBefore,
		"req-3", "gpt-5", "openai-codex-oauth",
		model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
		[]byte(`{}`))
	assert.Empty(t, buf.String())
}

// TestEmitDebugFailure_FieldsRoundTrip confirms the failure snapshot
// carries every documented field so operators can correlate the
// exact step that rejected the request. Stage=req.failed is the
// canonical label.
func TestEmitDebugFailure_FieldsRoundTrip(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	h := &Handler{}
	h.emitDebugFailure(
		context.Background(),
		"req-fail-1", "gpt-5.6-sol", "openai-codex-oauth",
		model.FORMAT_ANTHROPIC_MESSAGES, model.FORMAT_OPENAI_RESPONSES,
		"invalid_request", "invalid_request", "missing required field model",
		[]byte(`{"messages":[]}`),
	)
	out := buf.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, `"stage":"req.failed"`)
	assert.Contains(t, out, `"request_id":"req-fail-1"`)
	assert.Contains(t, out, `"model":"gpt-5.6-sol"`)
	assert.Contains(t, out, `"provider":"openai-codex-oauth"`)
	assert.Contains(t, out, `"source_format":"anthropic-messages"`)
	assert.Contains(t, out, `"target_format":"openai-responses"`)
	assert.Contains(t, out, `"error_code":"invalid_request"`)
	assert.Contains(t, out, `"error_kind":"invalid_request"`)
	assert.Contains(t, out, `"error_message":"missing required field model"`)
	assert.Contains(t, out, `"body_bytes":15`)
	assert.Contains(t, out, `"body_truncated":false`)
}

// TestEmitDebugFailure_NilHandlerSafe guards against accidental
// nil-receiver calls on the failure path.
func TestEmitDebugFailure_NilHandlerSafe(t *testing.T) {
	buf := withSlogTee(t, slog.LevelDebug)
	var h *Handler
	h.emitDebugFailure(
		context.Background(),
		"req-fail-2", "", "", model.FORMAT_ANTHROPIC_MESSAGES, "",
		"unknown", "internal", "no body", nil,
	)
	assert.Empty(t, buf.String())
}

// TestDebugErrorInfo_ExtractsFromProxyError covers the error-info
// helper used by every internal writeError site. It must surface
// ProxyError.Code/Kind/Message and fall back to "unknown" for
// arbitrary errors so the failure snapshot has structured fields.
func TestDebugErrorInfo_ExtractsFromProxyError(t *testing.T) {
	t.Run("proxy_error", func(t *testing.T) {
		pe := &model.ProxyError{
			Kind:    model.ERROR_INVALID_REQUEST,
			Code:    "invalid_request",
			Message: "bad input",
			Cause:   errors.New("underlying"),
		}
		code, kind, msg := debugErrorInfo(pe)
		assert.Equal(t, "invalid_request", code)
		assert.Equal(t, "invalid_request", kind)
		assert.Equal(t, "bad input", msg)
	})
	t.Run("plain_error", func(t *testing.T) {
		code, kind, msg := debugErrorInfo(errors.New("boom"))
		assert.Equal(t, "unknown", code)
		assert.Equal(t, "internal", kind)
		assert.Equal(t, "boom", msg)
	})
	t.Run("nil_error", func(t *testing.T) {
		code, kind, msg := debugErrorInfo(nil)
		assert.Equal(t, "unknown", code)
		assert.Equal(t, "internal", kind)
		assert.Equal(t, "", msg)
	})
}

// TestTruncateBytes covers the edge cases of the truncation helper.
func TestTruncateBytes(t *testing.T) {
	t.Run("under_limit", func(t *testing.T) {
		b, trunc := truncateBytes([]byte("hello"), 10)
		assert.False(t, trunc)
		assert.Equal(t, []byte("hello"), b)
	})
	t.Run("at_limit", func(t *testing.T) {
		b, trunc := truncateBytes([]byte("hello"), 5)
		assert.False(t, trunc)
		assert.Equal(t, []byte("hello"), b)
	})
	t.Run("over_limit", func(t *testing.T) {
		b, trunc := truncateBytes([]byte("hello world"), 5)
		assert.True(t, trunc)
		assert.Equal(t, []byte("hello"), b)
	})
	t.Run("zero_limit", func(t *testing.T) {
		b, trunc := truncateBytes([]byte("hello"), 0)
		assert.True(t, trunc)
		assert.Empty(t, b)
	})
}

// itoa is a tiny local helper to avoid pulling strconv into the test
// file's imports for a single sprintf-like use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}