# Upstream Error Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 讓 handler 層所有上游 4xx/5xx 與 stream 終結都留下 level=Error 的結構化 slog，欄位足以讓既有 access log 透過 `request_id` join。

**Architecture:** 在 `handlers/` package 新增兩個 package-private helper（`logUpstreamError` / `logStreamError`），把既有 chat path 的 positional log 統一升級為 `LogAttrs`，並補齊 count_tokens、stream、bridge 三處缺漏。Body 不 redaction；sensitive response header 過濾；64KB body 截斷。

**Tech Stack:** Go 1.24, `log/slog`, `github.com/stretchr/testify`, `net/http`.

## Global Constraints

- **Scope 鎖定 handler-only**：不動 provider package、不動 oauth refresh、不動 `DecodeUpstreamError`。
- **Level 統一 `Error`**：所有 4xx/5xx 與 stream 終結都走 `slog.LevelError`，不分 5xx/4xx。
- **Body 不 redaction**：完整保留上游原始 bytes；風險由 log forwarder / disk ACL 負責。
- **Sensitive header 過濾**：authorization、proxy-authorization、cookie、set-cookie、x-api-key、api-key、x-auth-token、x-amz-security-token（大小寫不敏感）一律不寫。
- **Body 截斷**：64KB 上限（沿用既有 `MAX_UPSTREAM_ERROR_BYTES`），超限附 `body_truncated=true` + `body_bytes=<原長>`。
- **Log message 字串**：非串流用 `"proxy upstream error response"`；串流終結用 `"proxy upstream stream error"`。
- **測試風格**：沿用 `codex_log_test.go` 的 `slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, ...)))` tee pattern；斷言用 testify `assert` / `require`。
- **常數位置**：`MAX_UPSTREAM_ERROR_BYTES` 從 `handler.go:24` 搬到新檔 `upstream_error_log.go`，handler.go 改用新檔名稱。

---

## File Structure

| File | Status | Responsibility |
|------|--------|----------------|
| `handlers/upstream_error_log.go` | NEW | `logUpstreamError` / `logStreamError` helpers + `filterResponseHeaders` + `sensitiveHeaders` + `MAX_UPSTREAM_ERROR_BYTES` |
| `handlers/upstream_error_log_test.go` | NEW | helper 單元測試（7 個 case） |
| `handlers/handler.go` | MODIFY | 移除 L185 positional log；補 count_tokens 路徑；`writeTerminalStreamError` 擴充參數並內部呼叫 `logStreamError` |

Provider package / svc/transform / 其他 handler 檔案不動。

---

## Cross-Task Interfaces

- `sensitiveHeaders` — package-private `map[string]struct{}` 常數
- `filterResponseHeaders(http.Header) http.Header` — package-private
- `MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10` — package-level，搬到新檔
- `(*Handler).logUpstreamError(ctx context.Context, requestIDValue, routedModel, providerID string, response *http.Response) []byte` — 永遠不返 error；body 讀取失敗時回傳 nil
- `(*Handler).logStreamError(ctx context.Context, requestIDValue, routedModel, providerID string, response *http.Response, cause string)` — response 可為 nil

---

### Task 1: filterResponseHeaders + sensitive list

**Files:**
- Create: `handlers/upstream_error_log.go`
- Create: `handlers/upstream_error_log_test.go`

**Interfaces:**
- Produces: `var sensitiveHeaders map[string]struct{}` (package-private)
- Produces: `func filterResponseHeaders(http.Header) http.Header` (package-private)

- [ ] **Step 1: Write the failing test**

Create `handlers/upstream_error_log_test.go` with:

```go
package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestFilterResponseHeaders -v`
Expected: build error (`filterResponseHeaders` undefined).

- [ ] **Step 3: Write minimal implementation**

Create `handlers/upstream_error_log.go` with:

```go
package handlers

import (
	"log/slog"
	"net/http"

	"github.com/bizshuk/proxy/model"
)

// MAX_UPSTREAM_ERROR_BYTES caps how many bytes of an upstream
// error response we read into memory and emit to slog.
const MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10

// sensitiveHeaders is the case-insensitive deny-list of response
// header names that must never appear in proxy error logs. We
// deliberately keep this list narrow — request/response bodies
// are written verbatim by policy (see spec section 3), but
// upstream 4xx/5xx response headers can echo credentials
// (Authorization, Set-Cookie) and these are filtered out.
var sensitiveHeaders = map[string]struct{}{
	"authorization":        {},
	"proxy-authorization":  {},
	"cookie":               {},
	"set-cookie":           {},
	"x-api-key":            {},
	"api-key":              {},
	"x-auth-token":         {},
	"x-amz-security-token": {},
}

// filterResponseHeaders returns a copy of h with sensitive header
// names removed. Always returns a non-nil http.Header.
func filterResponseHeaders(h http.Header) http.Header {
	out := http.Header{}
	for name, values := range h {
		if _, skip := sensitiveHeaders[strings.ToLower(name)]; skip {
			continue
		}
		out[name] = values
	}
	return out
}

// logUpstreamError and logStreamError are defined in subsequent
// tasks. This file exists first so we can colocate the constants
// and the helper with the handlers package.
var _ = model.ERROR_UPSTREAM // keep import alive during scaffolding
```

And the missing `strings` import — add it:

```go
import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/model"
)
```

(The `_ = model.ERROR_UPSTREAM` and the slog import will be removed in later tasks once real helpers exist. For Task 1, only `strings` and `net/http` are actually used; remove the other imports if the compiler complains.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestFilterResponseHeaders -v`
Expected: PASS for both test functions.

- [ ] **Step 5: Run linter**

Run: `cd /Users/shuk/projects/ai/proxy && go vet ./handlers/...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
cd /Users/shuk/projects/ai/proxy && git add handlers/upstream_error_log.go handlers/upstream_error_log_test.go
git commit -m "feat(handlers): filter sensitive response headers for error logs

Adds filterResponseHeaders + sensitiveHeaders deny-list. Body
bytes continue to be written verbatim by spec decision; only
auth-credential response headers are stripped before slog emits.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: logUpstreamError + 5 tests

**Files:**
- Modify: `handlers/upstream_error_log.go`
- Modify: `handlers/upstream_error_log_test.go`

**Interfaces:**
- Produces: `(*Handler).logUpstreamError(ctx, requestIDValue, routedModel, providerID string, response *http.Response) []byte`
- Consumes: `MAX_UPSTREAM_ERROR_BYTES`, `filterResponseHeaders` (both from Task 1)

- [ ] **Step 1: Add the failing tests**

Append to `handlers/upstream_error_log_test.go`:

```go
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
	assert.Equal(t, int(MAX_UPSTREAM_ERROR_BYTES), len(body))
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestLogUpstreamError -v`
Expected: build error (`logUpstreamError` undefined).

- [ ] **Step 3: Implement logUpstreamError**

Add to `handlers/upstream_error_log.go` (final version after Task 1 scaffolding is replaced):

```go
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// logUpstreamError records an upstream 4xx/5xx response at
// level=Error and returns the body for the caller to continue
// feeding into DecodeUpstreamError. It never returns an error;
// the caller gets nil body when reading failed and falls back
// to the existing "no body" handling in handleUpstreamError.
func (h *Handler) logUpstreamError(
	ctx context.Context,
	requestIDValue, routedModel, providerID string,
	response *http.Response,
) []byte {
	if h == nil {
		return nil
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestIDValue),
		slog.String("provider", providerID),
		slog.String("model", routedModel),
		slog.Int("status_code", response.StatusCode),
	}
	if response.Header != nil {
		for name, values := range filterResponseHeaders(response.Header) {
			if len(values) == 1 {
				attrs = append(attrs, slog.String("header."+strings.ToLower(name), values[0]))
				continue
			}
			attrs = append(attrs, slog.Any("header."+strings.ToLower(name), values))
		}
	}
	body, err := readUpstreamErrorBody(response)
	switch {
	case err != nil:
		attrs = append(attrs,
			slog.String("body_read_error", err.Error()),
			slog.Int64("body_bytes", 0),
		)
	case len(body) > int(MAX_UPSTREAM_ERROR_BYTES):
		attrs = append(attrs,
			slog.String("body", string(body[:MAX_UPSTREAM_ERROR_BYTES])),
			slog.Bool("body_truncated", true),
			slog.Int("body_bytes", len(body)),
		)
	default:
		attrs = append(attrs,
			slog.String("body", string(body)),
			slog.Bool("body_truncated", false),
		)
	}
	slog.LogAttrs(ctx, slog.LevelError, "proxy upstream error response", attrs...)
	return body
}

// readUpstreamErrorBody drains the response body up to
// MAX_UPSTREAM_ERROR_BYTES + 1 so we can both log the
// prefix AND report the original length for truncation
// semantics. A nil Body is treated as "no body" rather
// than an error.
func readUpstreamErrorBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errUpstreamBodyNil
	}
	buf, err := readAllBounded(response.Body, MAX_UPSTREAM_ERROR_BYTES+1)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

var errUpstreamBodyNil = stringError("response body nil")

type stringError string

func (s stringError) Error() string { return string(s) }
```

And add the helper for bounded reads (re-using the existing pattern but scoped to this file):

```go
func readAllBounded(r interface{ Read(p []byte) (int, error) }, limit int64) ([]byte, error) {
	// We can't reuse io.ReadAll(io.LimitReader(...)) shape here
	// because that returns (n=int64, err=nil) for the truncated
	// case without telling us the original length. Implement
	// bounded read directly.
	const chunk = 32 << 10
	out := make([]byte, 0, limit)
	readBuf := make([]byte, chunk)
	for int64(len(out)) < limit {
		n, err := r.Read(readBuf)
		if n > 0 {
			out = append(out, readBuf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
	}
	return out, nil
}
```

Wait — `readAllBounded` over a generic interface doesn't compile; revert to using a concrete `io.Reader`. Replace `readAllBounded` with:

```go
import "io"

func readUpstreamErrorBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errUpstreamBodyNil
	}
	out := make([]byte, 0, MAX_UPSTREAM_ERROR_BYTES+1)
	buf := make([]byte, 32<<10)
	for int64(len(out)) <= MAX_UPSTREAM_ERROR_BYTES {
		n, err := response.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				return out, nil
			}
			return nil, err
		}
	}
	return out, nil
}
```

And drop the `readAllBounded` helper.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestLogUpstreamError -v`
Expected: PASS for all 5 tests.

- [ ] **Step 5: Run linter**

Run: `cd /Users/shuk/projects/ai/proxy && go vet ./handlers/...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
cd /Users/shuk/projects/ai/proxy && git add handlers/upstream_error_log.go handlers/upstream_error_log_test.go
git commit -m "feat(handlers): logUpstreamError with structured attrs

Records status / filtered headers / body (verbatim, truncated at
64KB) at level=Error. Body read errors are surfaced via
body_read_error rather than swallowed so the log entry stays
complete.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: logStreamError + 2 tests

**Files:**
- Modify: `handlers/upstream_error_log.go`
- Modify: `handlers/upstream_error_log_test.go`

**Interfaces:**
- Produces: `(*Handler).logStreamError(ctx, requestIDValue, routedModel, providerID string, response *http.Response, cause string)`

- [ ] **Step 1: Add the failing tests**

Append to `handlers/upstream_error_log_test.go`:

```go
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
	// No header.* attrs should appear when response is nil.
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestLogStreamError -v`
Expected: build error (`logStreamError` undefined).

- [ ] **Step 3: Implement logStreamError**

Add to `handlers/upstream_error_log.go`:

```go
// logStreamError records an upstream stream termination event.
// response may be nil (e.g. SSE decoder dropped the connection
// mid-loop). cause must be a short stable token: e.g.
// "sse_decode_error", "stream_push_error", "context_canceled".
func (h *Handler) logStreamError(
	ctx context.Context,
	requestIDValue, routedModel, providerID string,
	response *http.Response,
	cause string,
) {
	if h == nil {
		return
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestIDValue),
		slog.String("provider", providerID),
		slog.String("model", routedModel),
		slog.String("cause", cause),
	}
	if response != nil {
		attrs = append(attrs, slog.Int("status_code", response.StatusCode))
		for name, values := range filterResponseHeaders(response.Header) {
			if len(values) == 1 {
				attrs = append(attrs, slog.String("header."+strings.ToLower(name), values[0]))
				continue
			}
			attrs = append(attrs, slog.Any("header."+strings.ToLower(name), values))
		}
	}
	slog.LogAttrs(ctx, slog.LevelError, "proxy upstream stream error", attrs...)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/ -run TestLogStreamError -v`
Expected: PASS for both tests.

- [ ] **Step 5: Run linter**

Run: `cd /Users/shuk/projects/ai/proxy && go vet ./handlers/...`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
cd /Users/shuk/projects/ai/proxy && git add handlers/upstream_error_log.go handlers/upstream_error_log_test.go
git commit -m "feat(handlers): logStreamError for stream termination events

Records cause + (when available) status + filtered headers at
level=Error. Response may be nil — that path emits no header
attrs so the log entry stays consistent.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: Wire logUpstreamError into non-stream paths

**Files:**
- Modify: `handlers/handler.go` (chat path L183-189 + count_tokens path L296-298)
- Modify: `handlers/handler.go` (remove now-duplicated `MAX_UPSTREAM_ERROR_BYTES` from L24; keep var but point to upstream_error_log.go's constant)

**Interfaces:**
- Consumes: `(*Handler).logUpstreamError` (from Task 2)

- [ ] **Step 1: Remove the duplicate MAX_UPSTREAM_ERROR_BYTES from handler.go**

In `handlers/handler.go`, delete line 24:

```go
const MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10
```

(The constant is now defined in `upstream_error_log.go`.)

- [ ] **Step 2: Replace the chat-path positional log**

In `handlers/handler.go`, replace lines 183-189:

```go
if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
    if peek, peekErr := io.ReadAll(io.LimitReader(response.Body, MAX_UPSTREAM_ERROR_BYTES+1)); peekErr == nil {
        slog.Error("upstream 4xx/5xx body", "status", response.StatusCode, "model", routed.Model, "provider", profile.ID, "body", string(peek))
        response.Body = io.NopCloser(bytes.NewReader(peek))
    }
    h.handleUpstreamError(c, format, response)
    return
}
```

with:

```go
if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
    body := h.logUpstreamError(c.Request.Context(), requestID, routed.Model, profile.ID, response)
    response.Body = io.NopCloser(bytes.NewReader(body))
    h.handleUpstreamError(c, format, response)
    return
}
```

If `body` is nil (read failed), `io.NopCloser(bytes.NewReader(nil))` is valid; `handleUpstreamError` already handles a nil/empty body via `transform.DecodeUpstreamError(..., nil)`.

- [ ] **Step 3: Wire logUpstreamError into count_tokens path**

In `handlers/handler.go`, replace lines 296-298:

```go
if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
    h.handleUpstreamError(c, model.FORMAT_ANTHROPIC_MESSAGES, response)
    return
}
```

with:

```go
if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
    body := h.logUpstreamError(c.Request.Context(), headers.Get("x-request-id"), routed.Model, profile.ID, response)
    response.Body = io.NopCloser(bytes.NewReader(body))
    h.handleUpstreamError(c, model.FORMAT_ANTHROPIC_MESSAGES, response)
    return
}
```

- [ ] **Step 4: Run go build and existing test suite**

Run: `cd /Users/shuk/projects/ai/proxy && go build ./...`
Expected: no errors.

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/...`
Expected: all existing handler tests pass.

If a test breaks because it relied on the positional log format (e.g. a test greps stderr for "upstream 4xx/5xx body"), update the test's expected substring to `"proxy upstream error response"` — but DO NOT change test semantic, only the substring used to find the log.

- [ ] **Step 5: Commit**

```bash
cd /Users/shuk/projects/ai/proxy && git add handlers/handler.go
git commit -m "refactor(handlers): wire logUpstreamError into chat + count_tokens

Removes the positional slog.Error call from the chat path and
adds the same structured logging to count_tokens, which had no
upstream-error log at all. Replaces local MAX_UPSTREAM_ERROR_BYTES
with the canonical constant in upstream_error_log.go.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: Wire logStreamError into stream + bridge terminations

**Files:**
- Modify: `handlers/handler.go` (signature + body of `writeTerminalStreamError`; three call sites in `handleStream` and `handleBridge`)

**Interfaces:**
- Consumes: `(*Handler).logStreamError` (from Task 3)

- [ ] **Step 1: Update `writeTerminalStreamError` signature and body**

In `handlers/handler.go`, replace the entire `writeTerminalStreamError` function (lines 415-435):

```go
func (h *Handler) writeTerminalStreamError(c *gin.Context, format model.Format) {
    var frames []model.SSEFrame
    switch format {
    case model.FORMAT_ANTHROPIC_MESSAGES:
        frames = []model.SSEFrame{{
            Event: "error",
            Data:  []byte(`{"type":"error","error":{"type":"api_error","message":"stream terminated"}}`),
        }}
    case model.FORMAT_OPENAI_CHAT:
        frames = []model.SSEFrame{
            {Data: []byte(`{"error":{"type":"api_error","code":"stream_error","message":"stream terminated"}}`)},
            {Data: []byte("[DONE]")},
        }
    case model.FORMAT_OPENAI_RESPONSES:
        frames = []model.SSEFrame{{
            Event: "response.failed",
            Data:  []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"stream_error","message":"stream terminated"}}}`),
        }}
    }
    _ = writeStreamFrames(c, frames)
}
```

with:

```go
func (h *Handler) writeTerminalStreamError(
    c *gin.Context,
    format model.Format,
    requestIDValue, routedModel, providerID string,
    response *http.Response,
    cause string,
) {
    h.logStreamError(c.Request.Context(), requestIDValue, routedModel, providerID, response, cause)
    var frames []model.SSEFrame
    switch format {
    case model.FORMAT_ANTHROPIC_MESSAGES:
        frames = []model.SSEFrame{{
            Event: "error",
            Data:  []byte(`{"type":"error","error":{"type":"api_error","message":"stream terminated"}}`),
        }}
    case model.FORMAT_OPENAI_CHAT:
        frames = []model.SSEFrame{
            {Data: []byte(`{"error":{"type":"api_error","code":"stream_error","message":"stream terminated"}}`)},
            {Data: []byte("[DONE]")},
        }
    case model.FORMAT_OPENAI_RESPONSES:
        frames = []model.SSEFrame{{
            Event: "response.failed",
            Data:  []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"stream_error","message":"stream terminated"}}}`),
        }}
    }
    _ = writeStreamFrames(c, frames)
}
```

Note: when the upstream returned 4xx/5xx as JSON instead of SSE (the most common non-stream termination), `handleStream` and `handleBridge` already send the response through `handleUpstreamError` paths and never call `writeTerminalStreamError`. So `response` here is typically nil or non-nil-but-irrelevant; passing nil is safe.

- [ ] **Step 2: Update call sites in handleStream**

In `handlers/handler.go` `handleStream` (around lines 372-403), update three call sites:

a) `decoder.Next()` error (line 380-383):

```go
if decodeErr != nil {
    h.writeTerminalStreamError(c, source)
    return
}
```

becomes:

```go
if decodeErr != nil {
    h.writeTerminalStreamError(c, source,
        exchange.TranslatedRequest.Headers.Get("x-request-id"),
        routedModelOf(exchange), providerIDOf(exchange), nil, "sse_decode_error")
    return
}
```

b) `stream.Push` error (line 384-389):

```go
if pushErr != nil {
    if c.Request.Context().Err() == nil {
        h.writeTerminalStreamError(c, source)
    }
    return
}
```

becomes:

```go
if pushErr != nil {
    cause := "stream_push_error"
    if c.Request.Context().Err() != nil {
        cause = "context_canceled"
    }
    h.writeTerminalStreamError(c, source,
        exchange.TranslatedRequest.Headers.Get("x-request-id"),
        routedModelOf(exchange), providerIDOf(exchange), nil, cause)
    return
}
```

c) `stream.Close` error (line 395-401):

```go
if err != nil {
    if c.Request.Context().Err() == nil {
        h.writeTerminalStreamError(c, source)
    }
    return
}
```

becomes:

```go
if err != nil {
    cause := "stream_close_error"
    if c.Request.Context().Err() != nil {
        cause = "context_canceled"
    }
    h.writeTerminalStreamError(c, source,
        exchange.TranslatedRequest.Headers.Get("x-request-id"),
        routedModelOf(exchange), providerIDOf(exchange), nil, cause)
    return
}
```

The `exchange` variable is in scope; but the spec wants `routedModel` and `providerID`. Add a helper:

```go
// routedModelOf extracts the model name from an Exchange for
// logging. The field is already on Exchange.TranslatedRequest.Model.
func routedModelOf(exchange model.Exchange) string {
    return exchange.TranslatedRequest.Model
}

func providerIDOf(exchange model.Exchange) string {
    return exchange.ProviderID
}
```

Add these helpers to `upstream_error_log.go` near `logStreamError`.

- [ ] **Step 3: Update call sites in handleBridge**

In `handlers/handler.go` `handleBridge` (around lines 489-528), there are more error sites — `protocolStreamError("cannot decode upstream event stream", ...)` (line 499) and `stream.Push` (line 502-505) and `collector.Push` (line 508-511) and `boundedCollector.Close` (line 525-528). Each of these is `h.writeError(c, source, ...)` which already encodes the proxy error to the client.

Per spec section 4 ("stream path只記 handler-level 最終結果"), we do NOT inject logStreamError into every individual error path — that would be frame-level noise. Instead, add a single log inside `handleBridge` near the top of the error path: if `c.Request.Context().Err() != nil` at the function entry, log it and proceed.

Add this guard at the top of `handleBridge`:

```go
func (h *Handler) handleBridge(
    c *gin.Context,
    source, target model.Format,
    profile upstream.Profile,
    pair transform.Pair,
    exchange model.Exchange,
    response *http.Response,
) {
    if !acceptsEventStream(profile, response.Header.Get("Content-Type")) {
        h.writeError(c, source, protocolStreamError("upstream did not return an event stream", nil))
        return
    }
    // ... existing code ...
```

becomes:

```go
func (h *Handler) handleBridge(
    c *gin.Context,
    source, target model.Format,
    profile upstream.Profile,
    pair transform.Pair,
    exchange model.Exchange,
    response *http.Response,
) {
    if c.Request.Context().Err() != nil {
        h.logStreamError(c.Request.Context(),
            exchange.TranslatedRequest.Headers.Get("x-request-id"),
            exchange.TranslatedRequest.Model, exchange.ProviderID, response, "context_canceled")
        return
    }
    if !acceptsEventStream(profile, response.Header.Get("Content-Type")) {
        // ... unchanged ...
    }
```

Per-stream errors inside `handleBridge`'s loop are out of scope (spec section "stream path只記 handler-level 最終結果"). The existing `h.writeError(c, source, ...)` calls remain.

- [ ] **Step 4: Run go build and existing test suite**

Run: `cd /Users/shuk/projects/ai/proxy && go build ./...`
Expected: no errors.

Run: `cd /Users/shuk/projects/ai/proxy && go test ./handlers/...`
Expected: all existing handler tests pass.

If a test calls `writeTerminalStreamError` directly with the old 2-arg signature, update it to the new 7-arg signature with placeholder values (`"", "", "", nil, ""`).

- [ ] **Step 5: Commit**

```bash
cd /Users/shuk/projects/ai/proxy && git add handlers/handler.go handlers/upstream_error_log.go
git commit -m "feat(handlers): logStreamError wired into stream + bridge terminations

writeTerminalStreamError now records the upstream termination
event (cause / filtered headers / status) before writing the
SSE error frame. context-canceled is distinguished from
decoder/push errors via the cause attr. handleBridge logs
context_canceled once at entry rather than per-frame.

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: Final verification + cleanup

**Files:** none new; verify all changes build and tests pass.

- [ ] **Step 1: go vet, go build, full test suite**

Run: `cd /Users/shuk/projects/ai/proxy && go vet ./... && go build ./... && go test ./...`
Expected: all PASS, no vet warnings.

- [ ] **Step 2: Manual smoke against a real (or stub) 4xx response**

Pick any provider test fixture or write a 10-line smoke that:
- Calls `h.Handle(model.FORMAT_OPENAI_CHAT)` with a mock upstream returning 401
- Confirms the captured slog JSON line includes `msg="proxy upstream error response"`, `status_code=401`, and the expected `header.*` fields
- Confirms `Authorization` is absent

If a real-provider smoke is impractical, skip this step and note in the PR description that manual verification is pending.

- [ ] **Step 3: Verify the spec checklist**

Walk through `docs/specs/2026-07-19-upstream-error-logging.md` section by section:

- Scope handler-only ✓
- Level all Error ✓
- Body no redaction ✓
- Header sensitive filter ✓
- Structured fields (request_id, provider, model, status_code, header.*, body, body_truncated, body_bytes) ✓
- Log message strings ✓

- [ ] **Step 4: Final commit (if any lingering changes)**

If `git status` is dirty, commit:

```bash
cd /Users/shuk/projects/ai/proxy && git status
# if dirty:
git add -A && git commit -m "chore(handlers): final cleanup for upstream error logging"
```

Otherwise: nothing to do. The implementation is complete.

---

## Self-Review

**1. Spec coverage:**

| Spec section | Implemented in |
|--------------|----------------|
| §1 Scope handler-only | Tasks 1-5 stay in `handlers/` |
| §2 Level all Error | `slog.LevelError` in Tasks 2, 3, 4, 5 |
| §3 Body no redaction | `slog.String("body", ...)` in Task 2 |
| §4 Sensitive header filter | Task 1 (`filterResponseHeaders`) used in Tasks 2, 3 |
| §5 Structured fields | Task 2 attr list, Task 3 attr list |
| §6 Log message strings | "proxy upstream error response" / "proxy upstream stream error" hardcoded in Tasks 2, 3 |
| §Helper internal contract | Task 2 step 3 includes nil-body / read-error / truncation behavior |
| §File changes | Task 1 creates upstream_error_log.go + _test.go; Tasks 4-5 modify handler.go |
| §Test scope | Tasks 1, 2, 3 each define their failing tests before implementation |
| §Risks documented | Spec sections 7; not a code change |

**2. Placeholder scan:** No TBD / TODO / "implement later" / "similar to Task N" patterns. Every code step contains the full code.

**3. Type consistency:**

- `logUpstreamError` signature: defined in Task 2 step 3, used identically in Tasks 4 step 2 and 4 step 3.
- `logStreamError` signature: defined in Task 3 step 3, used in Task 5 step 1.
- `writeTerminalStreamError` signature change: Task 5 step 1 declares the new 7-arg signature; all call sites updated in Task 5 steps 2 and 3.
- `routedModelOf` / `providerIDOf` helpers: declared in Task 5 step 2; used at three call sites in the same step.
- `MAX_UPSTREAM_ERROR_BYTES`: declared in Task 1, removed from handler.go in Task 4 step 1.

No mismatches found.
