# Provider 上游錯誤回應日誌強化 — Design Spec

- 日期: 2026-07-19
- 狀態: Draft (brainstorming → writing-plans)

## 問題陳述

`handlers/handler.go` 目前在 chat 路徑（L185）有 positional `slog.Error("upstream 4xx/5xx body", ...)`，
但 count_tokens 路徑（L296-298）、stream 中斷、bridge 失敗**完全沒有任何 slog 輸出**。
Provider package 內的 completion / oauth refresh 失敗也僅以 `fmt.Errorf` 回傳，不直接 log。
當下游客戶端回報「這個 request 沒成功」時，operator 在 Loki / log 檔內沒有對應紀錄可查。

## 目標

所有上游 4xx/5xx 在 handler 層都留下 `level=Error` 的結構化紀錄，欄位足以讓既有 access log 透過 `request_id` join。
非 4xx/5xx 的 stream 中斷（例如 decoder 錯、push 錯、context 取消）也留一筆 `level=Error`，但只記最終結果，
不在每個 mid-stream frame 重複記。

## 非目標 (Out of Scope)

- Provider package 內的 completion / oauth refresh 失敗 log（本 spec 不動）
- `DecodeUpstreamError` 的轉換邏輯
- 既有的 `proxy request routed` / `proxy request completed` info log
- log forwarder / Loki 端的 alert 規則
- 請求端 request body 的 log（本 repo 既有 `codex_log.go` 已處理 codex 摘要）

## 設計決定

### 1. Scope：handler-only

所有變動集中在 `handlers/` package。Provider 與 oauth flow 維持原樣。
理由：provider 的 `fmt.Errorf` 已帶足夠 context（status + body），由 handler 統一 log 避免每個 provider 重複實作。

### 2. Level：所有 4xx/5xx 走 `Error`

不分 5xx / 4xx、不分 client / server。
理由：操作員看的是「上游回非成功」這件事；4xx 與 5xx 在 query 與 alert 規則可再用 status_code 區分。
Stream 終結（包括 context canceled）也走 `Error`。

### 3. Body：不 redaction

完全保留上游原始 body bytes，包含上游 echo 出的任何內容（prompt 片段、metadata、request_id）。
**已知風險**：若上游在 4xx/5xx body 內 echo API key / PII，這些會進 log。
緩解責任在 log forwarder / disk ACL，不在 proxy 本體。

### 4. Header：過濾敏感 header

response header 中以下名稱（大小寫不敏感）**不寫進 log**：

```
authorization
proxy-authorization
cookie
set-cookie
x-api-key
api-key
x-auth-token
x-amz-security-token
```

其他 header 一律寫，以 `header.<name>` 為 attr key。

### 5. 結構化欄位

每筆 log 包含以下 attr（依序）：

| attr key | 類型 | 說明 |
|----------|------|------|
| `request_id` | string | 來自 `x-request-id` 或自動產生（沿用既有 `requestID()` helper） |
| `provider` | string | `profile.ID` |
| `model` | string | routing 後的 `routed.Model` |
| `status_code` | int | HTTP status code |
| `header.<name>` | string | 過濾敏感 header 後的每個 header |
| `body` | string | 上游 response body（截斷後） |
| `body_truncated` | bool | body 超過 64KB 時為 `true` |
| `body_bytes` | int | 原始 body 長度 |
| `body_read_error` | string | body 讀取失敗時填入（成功時省略） |

stream 終結的 log 不會有 `body` / `body_truncated` / `body_bytes`，改記：

| attr key | 類型 | 說明 |
|----------|------|------|
| `cause` | string | 例 `sse_decode_error` / `stream_push_error` / `context_canceled` |
| `header.<name>` | string | 來自 response header（若有；response 為 nil 時整段省略） |

### 6. Log message 字串

非串流：`"proxy upstream error response"`
串流終結：`"proxy upstream stream error"`

固定字串讓 Loki 可用 `msg="proxy upstream error response"` group by。

## 檔案變動

### 新增

- `handlers/upstream_error_log.go`
  - `func (h *Handler) logUpstreamError(ctx, requestIDValue, routedModel, providerID string, response *http.Response) []byte`
  - `func (h *Handler) logStreamError(ctx, requestIDValue, routedModel, providerID string, response *http.Response, cause string)`
  - `func filterResponseHeaders(h http.Header) http.Header`
  - `var sensitiveHeaders map[string]struct{}`（套件級常數）
  - `const MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10`（從 handler.go 移至此檔，但保留 `handler.go` 的 alias 或 import）

- `handlers/upstream_error_log_test.go`
  - `TestLogUpstreamError_IncludesAllFields`
  - `TestLogUpstreamError_FiltersSensitiveHeaders`
  - `TestLogUpstreamError_TruncatesBody`
  - `TestLogUpstreamError_NilBodyNoCrash`
  - `TestLogUpstreamError_BodyReadError`
  - `TestLogStreamError_MissingResponse`
  - `TestLogStreamError_IncludesFrameData`

### 修改

- `handlers/handler.go`
  - **L24** `MAX_UPSTREAM_ERROR_BYTES`：移除（搬到新檔）或保留 alias。採「搬到新檔並從新檔 export」避免重複常數。
  - **L183-189**（chat 4xx/5xx）：移除既有 positional `slog.Error` 區塊，改呼叫 `h.logUpstreamError(...)` 然後 reset `response.Body`。
  - **L296-298**（count_tokens 4xx/5xx）：在呼叫 `handleUpstreamError` 前先呼叫 `h.logUpstreamError(...)` 並 reset `response.Body`。
  - **`writeTerminalStreamError`**：改為接收 `(c *gin.Context, requestIDValue, routedModel, providerID string, response *http.Response, cause string)`；
    在寫 SSE 終結 frame 之前呼叫 `h.logStreamError(...)`。
  - **`handleStream` / `handleBridge`**：呼叫 `writeTerminalStreamError` 的三處（decoder 錯、push 錯、close 錯）帶入上述參數。
  - 既有 L113 / L134 / L260 的 info log 不動。

## Helper 內部契約

`logUpstreamError`：

| 情境 | 行為 |
|------|------|
| `response.Body` 為 nil | 寫 `body=""`、`body_read_error="response body nil"`、`body_bytes=0`，回傳 nil |
| `io.ReadAll` 失敗 | 寫 `body=""`、`body_read_error=<err>`，回傳 nil |
| body ≤ 64KB | 寫完整 body、`body_truncated=false`、`body_bytes=<len>`，回傳 body |
| body > 64KB | 寫前 64KB、`body_truncated=true`、`body_bytes=<原始長度>`，回傳 truncated body |
| `response.Header` 為 nil | 略過所有 `header.*` attr |
| ctx 已 canceled | 仍寫 log（slog fallback 到背景 logger），不報錯 |

`logUpstreamError` 永遠不返回 error。caller 拿到 nil 表示「沒東西餵 DecodeUpstreamError」，
既有 `handleUpstreamError` 已對 nil body 有 handling。

`logStreamError` 同樣不返回 error，且 response 可為 nil（stream loop 可能已丟失）。
若 `response` 為 nil，整段 `header.*` 省略、`cause` 必填。

## 測試策略

所有 helper 測試用 `slog.New(slog.NewJSONHandler(&bytes.Buffer{}, ...))` 抓 JSON 行，
parse 後 assert 個別 attr 存在/不存在。沿用既有測試慣例：使用 `github.com/stretchr/testify`（handler_test.go、codex_log_test.go 等已採用），斷言用 `assert` / `require`。

## 風險與緩解

| 風險 | 緩解 |
|------|------|
| Body 內含 PII / API key 被寫進 log | 文件明示（見 section 3），由 log forwarder / disk ACL 負責 |
| 64KB body 對某些 streaming error frame 仍然不夠 | 已截斷、`body_truncated=true` 提示 |
| Header 名稱 collision（多值） | `filterResponseHeaders` 採 `http.Header` 原生型別保留多值 |
| Helper 測試與 handler_test.go 風格不一 | 寫 spec 時確認既有測試風格 |

## 開放問題

無。所有 scope / level / redaction / 欄位 / 呼叫點都已敲定。

## 自審結果 (Self-Review)

- Placeholder 掃描：無 TBD / TODO / FIXME / 佔位段落。
- 內部一致性：section 4 helper 契約與 section 5 檔案變動的 helper 簽名一致。
- Scope 檢查：單一檔案 package 變動 + 一個新檔 + 一個新 test 檔，適合單一 implementation plan。
- 模糊性：stream helper 的 response 為 nil 行為在 section 5 明示「整段 header.* 省略、cause 必填」。
