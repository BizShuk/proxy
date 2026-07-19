# Plan: 平鋪 auth × provider，proxy 改用 provider package

## Context

**目前痛點**
- `agentsdk/provider/` 內的 `anthropic/`、`google/`、`openaicompat/` 三個 module 在實作內把 SDK 與 `core.Message` 互轉，相當於「provider 內自己做 compat」。
- 每個 provider 的 wire-format DTO 散落在 `proxy/model/{anthropic,chat,responses}/`，而 `proxy/svc/upstream/profile.go` 又另開了 `Profile` / `Catalog` registry 來列舉 base URL / endpoint / auth scheme / header whitelist，等於把 provider 資訊再描述一次。
- `auth/` submodule 內的 `auth/provider/{anthropic,openai,google,xai,antigravity,vertex}` 是另一份 6 個 provider 包，與 `agentsdk/provider/` 的 3 個不重疊但職責混雜。

**目標**
- 把「auth × provider」整到 `provider/<name>/` 單一 package 內，**每個 package 自己擁有**
  - DTO (request/response 進/出 wire format)
  - DTO validation
  - auth (API key + OAuth 兩種 flavor 各一個檔，視 provider 而定)
  - stream parser
  - model catalog
- 最外層只訂一個 `provider.Provider` interface，實作內不再 wrap SDK + 不再做 compat
- proxy 改成直接 `import` 這些 provider package，不再維護自己的 `Profile` / `Catalog`

**實作順序**
1. 重構 `agentsdk/provider/` 為 5 個 flat module（本計畫 primary deliverable）
2. 把 `agentsdk/core/port.go` 的 `ModelProvider` interface 擴充為可攜帶 auth / 列舉 models / 取原生 DTO 流事件
3. 把 `proxy/svc/upstream/` 與 `proxy/model/{anthropic,chat,responses}/` 改成呼叫新 provider package；`Profile` / `Catalog` 整段刪掉
4. 不 commit，verify 通過後停下來等使用者 review

**參考**
- `https://github.com/earendil-works/pi/tree/main/packages/ai`：每個 provider 是 `providers/<name>.ts` + `providers/<name>.models.ts` + `auth/oauth/<name>.ts`，頂層 `Provider<TApi>` interface 自帶 `models` / `api` / `auth` 三欄位
- pi/types.ts 暴露 `Provider.auth` 為 `ProviderAuth { apiKey?: ApiKeyAuth; oauth?: OAuthAuth }`，每個 provider 各自擁有 `envApiKeyAuth` / `lazyOAuth` 兩種 helper

---

## 目標模組結構

```text
agentsdk/provider/
├── anthropic/                      # module：含 API + OAuth 兩個 sub-package
│   ├── go.mod
│   ├── provider.go                # New(...) → *Provider，實作 provider.Provider
│   ├── dto.go                     # Messages 進/出 DTO + ContentBlock / ToolUse / ToolResult
│   ├── validate.go                # dto.Validate()
│   ├── auth_api.go                # API-key auth：ANTHROPIC_API_KEY / store key
│   ├── auth_oauth.go              # OAuth：PKCE S256 + anthropic-dangerous-direct-browser-access + refresh
│   ├── stream.go                  # SSE 解析 + thinking + tool_use 折疊
│   ├── models.go                  # 靜態 catalog（claude-* family，含 reasoning/input profile）
│   └── options.go                 # WithAPIKey / WithOAuth / WithBaseURL / WithModel
├── ollama/                        # module：API-only（local，無 OAuth）
│   ├── go.mod                     # 純 stdlib（net/http + JSON + SSE）
│   └── ...                       # 同上骨架但只有 auth_api.go
├── grok/                          # module：API + OAuth（xAI / Grok）
│   ├── go.mod
│   └── ...
├── antigravity/                   # module：API + OAuth（Google OAuth 反代,callback 51121）
│   ├── go.mod
│   └── ...
└── codex/                         # module：API + OAuth（OpenAI ChatGPT Plus/Pro,chatgpt.com/backend-api）
    ├── go.mod
    └── ...
```

```text
agentsdk/core/port.go               # 擴充後的 outer interface
agentsdk/core/model.go              # 共用 Message/Part/Role/TokenUsage（從 provider 平移）
agentsdk/core/auth.go               # 共用 Auth carrier（key/pass_token/refresh_token），給 Provider 取用
```

```text
proxy/
├── svc/upstream/
│   └── dispatcher.go               # model name → 對應 provider package + auth resolve
│                                   # （取代 Profile/Catalog）
├── model/                          # 只保留 envelope / SSE 共用工具 + ProxyError
│                                   # （3 個 wire format sub-package 砍掉，由各 provider 自帶）
└── handlers/handler.go             # 3 個 client format × 5 個 provider 仍是 3×5 對接，
                                    # 但 transform logic 改成「呼叫 provider 自帶的 ToNative/FromNative」
```

---

## 關鍵設計決定

### 1. Outer interface（`agentsdk/provider/port.go` 新檔）

```go
package provider

type Auth struct {
    APIKey   string
    Bearer   string            // OAuth access_token（Bearer 形式送）
    Headers  map[string]string // 補 header（antigravity-google-oauth、codex-account-id）
    BaseURL  string            // optional override
}

type Model struct {
    ID       string
    Family   string
    Reasoning bool
    Input    []Modality        // text / image
    ContextWindow int
    MaxTokens int
}

type Provider interface {
    ID() string                                 // 例如 "anthropic"
    Models() []Model
    AuthSchemes() []string                      // 例如 ["api_key","oauth"]；ollama 只有 ["keyless"]
    Generate(ctx context.Context, req Request, auth Auth) (Result, error)
    Stream(ctx context.Context, req Request, auth Auth) (<-chan Chunk, error)
    CountTokens(ctx context.Context, msgs []Message) (int, error)
}

type Request struct {
    Model    string
    Messages []Message
    Tools    []ToolSpec
    MaxTokens int
    // 不帶 wire-format-specific 欄位（temperature、topP 等歸 provider 預設）
}

type Result struct { /* Text / ToolCalls / StopReason / Usage */ }
type Chunk   struct { /* Kind / Text / ToolUse / Done */ }
```

### 2. 每個 provider 內部 message 形狀

不再需要 `core.Message` ↔ `anthropic.MessageParam` 的雙向翻譯 — `provider.<name>` 直接用自己原生 DTO 拼出 request body、SSE 直接吐原生 stream event，由 `proxy/svc/upstream/dispatcher` 翻成 client wire format。

**重點：移掉 `agentsdk/provider/<name>/provider.go` 內所有「convert to core.Message」的 helper**。

### 3. 5 個 provider package 各自要處理的怪癖（維持 protocol 正確）

| Provider | Auth header / 端點 | 特殊處理 |
|---|---|---|
| `anthropic` (api) | `x-api-key: <key>` to `https://api.anthropic.com/v1/messages` | 無 |
| `anthropic` (oauth) | `Authorization: Bearer <token>` + `anthropic-dangerous-direct-browser-access: true` + `anthropic-beta: oauth-2025-04-20` | 同上 |
| `ollama` | 無 key，POST `http://localhost:11434/v1/chat/completions`（OpenAI-compat） | keyless；default base URL |
| `grok` (api) | `Authorization: Bearer <key>` to `https://api.x.ai/v1` | 純 OpenAI-compat |
| `grok` (oauth) | `Authorization: Bearer <token>` to `https://api.x.ai/v1` + `loginLabel: "Sign in with X Premium"` | 同上 |
| `antigravity` | Google OAuth 流程，callback `:51121`，打到 Google 端點變體 | device flow |
| `codex` | `Authorization: Bearer <token>` to `https://chatgpt.com/backend-api/codex/responses` + `ChatGPT-Account-ID` + `originator: codex_cli_rs` | 同 proxy 目前 `openai-codex-oauth` profile |

### 4. 刪除的舊東西

| 舊檔 | 改去哪 |
|---|---|
| `agentsdk/provider/anthropic/provider.go` 內 `toAnthropicMessages` / `fromAnthropicResponse` | 改用 `dto.go`（直接組 `anthropic.MessageParam`，runtime 不再二次翻譯） |
| `agentsdk/provider/google/provider.go` 類似的翻譯 helper | 同上 |
| `agentsdk/provider/openaicompat/provider.go` `flatt enMessage` 等 | 同上 |
| `proxy/svc/upstream/profile.go`（500+ 行，`DefaultCatalog()`） | 整段刪除，改成 `dispatcher.go` 內直接呼叫 `provider/<name>.New(...)` |
| `proxy/svc/upstream/client.go` | 簡化：變成 dispatcher 的 helper |
| `proxy/model/anthropic/`、`proxy/model/chat/`、`proxy/model/responses/` | 由各 provider package 自帶 DTO；proxy 只留 `model.RequestEnvelope` 共用 |
| `proxy/svc/transform/`（9 對 request/response/stream 檔） | 改成各 provider 提供 `Encode/Decode` 兩個 helper，由 dispatcher 呼叫 |

### 5. Auth 共享（不在 user 這波要求，但要注意）

- provider 內部只負責「給我 auth 就會通」，不管 auth 從哪來
- 之後若要讓 proxy 或 runtime 統一用同一份 `CredentialStore`（讀 ~/.config/agentSDK/auth.json），可以再加一層 `agentsdk/auth/` package，**先不實作**，因為 user 沒指定

---

## 實作步驟

### Phase A：擴充 outer interface（agentsdk）

1. **新檔** `agentsdk/provider/port.go`
   - 定義 `Provider` / `Auth` / `Model` / `Request` / `Result` / `Chunk` / `Message` / `ToolSpec`
   - `Auth` 結構對齊 pi 的 `ModelAuth`（apiKey / headers / baseUrl）
2. **改** `agentsdk/core/port.go`
   - 移除 `ModelProvider` interface（保留 `StateStore` / `WriteAheadLog` / `ToolRegistry` / `Tool` / `Notifier` / `ObservationSource`）
   - 把原本放在 `core/input.go` 的 `Message` / `Part` / `Role` / `TokenUsage` / `ModelChunk` / `ModelResult` / `ToolUseChunk` 平移到 `provider/port.go` 並 re-export（讓 runtime import 路徑短一點）
   - 保留 `core.Tool` / `core.ToolSpec` 不變

### Phase B：建立 5 個 provider module（agentsdk/provider/）

3. **新 module** `agentsdk/provider/anthropic/`
   - `dto.go` — 原生 Messages 進/出 DTO（request body、response、stream event，全部用真實 wire shape）
   - `validate.go` — request body schema 檢查
   - `auth_api.go` — `WithAPIKey` 或讀 `ANTHROPIC_API_KEY` / `ANTHROPIC_OAUTH_TOKEN`
   - `auth_oauth.go` — PKCE S256、device 流程、token refresh、組 3 個 OAuth header
   - `stream.go` — SSE 解析 + `message_start` / `content_block_delta` / `message_delta` → `Chunk` 折疊
   - `models.go` — `claude-opus-4-8` / `claude-sonnet-5` / `claude-haiku-4-5` 列表（含 `Reasoning`/`Input` profile）
   - `provider.go` — `New(opts...)` 回 `*anthropic.Provider` 實作 `provider.Provider`
4. **重命名/刪除** `agentsdk/provider/google/` → 併入 antigravity / 不留（依 user 清單決定要不要 google 純 Gemini 介面；user 沒列「google」這項，跳過）
5. **重新切** `agentsdk/provider/openaicompat/` → 改名 `agentsdk/provider/ollama/`
   - 內含 OpenAI-compat wire format（DTO 留）
   - 加 `auth_api.go`（keyless，env `OPENAI_API_KEY` 為 optional）
   - 增加 catalog：`llama3.2` / `qwen2.5` / `mistral` 等常見
6. **新 module** `agentsdk/provider/grok/`
   - 結構同 anthropic，但 base URL `https://api.x.ai/v1`
   - 兩種 auth：API key + OAuth（X Premium / SuperGrok）
   - models：grok-3 / grok-3-mini / grok-4
7. **新 module** `agentsdk/provider/antigravity/`
   - base URL 待 wire-format catalog 補（先用 `https://antigravity.googleapis.com/v1` placeholder + TODO）
   - 兩種 auth：API key + Google OAuth device flow
   - models：claude-* / gemini-*（CLIProxyAPI 揭露）
8. **新 module** `agentsdk/provider/codex/`
   - base URL `https://chatgpt.com/backend-api`
   - 兩種 auth：API key（不實際 open）+ OAuth（ChatGPT Plus/Pro）
   - 加 `ChatGPT-Account-ID` / `originator: codex_cli_rs` / `User-Agent: codex_cli_rs/<version>`
   - models：gpt-5 / gpt-5-mini

### Phase C：proxy 改呼叫新 providers

9. **刪** `proxy/svc/upstream/profile.go`（500+ 行 Profile/Catalog）
10. **新檔** `proxy/svc/upstream/dispatcher.go`
    - `Dispatcher` struct：持有所有 5 個 `provider.Provider`
    - `Resolve(modelName string) (provider.Provider, error)` — 從 model name 找 provider
11. **改** `proxy/svc/upstream/client.go`
    - 把 HTTP 送出邏輯抽成 `doOnce(ctx, provider, auth, body) (*http.Response, error)`
    - 接受 `provider.Provider` 介面後讓各 provider 自己組 header（內含 OAuth beta header 等）
12. **改** `proxy/handlers/handler.go`
    - 移除 wire-format 解碼（呼叫 `proxy/model/` 三個子 package）
    - 改為：依 client request format 解碼 → 拿到 `provider.Request`（由各 provider 自帶的 decoder 翻）→ 送 provider.Stream → 折疊回 client format
13. **刪** `proxy/model/{anthropic,chat,responses}/`（每個 package 約 200~500 行）
    - 改為保留 `proxy/model/envelope.go`（`RequestEnvelope` 共用型）+ SSE 共用 parser
14. **改** `proxy/svc/transform/`（9 對轉換）
    - 每對轉換縮減成 2 helper：`Encode(*provider.Request, format) ([]byte, error)` + `Decode([]byte, format) (*provider.Request, error)` 由各 provider 自帶
    - `transform/registry.go` 改成「format × provider」的 lookup，呼叫對應 helper
15. **改** `proxy/config/` 與 `proxy/svc/route/`
    - `route.Profile` 仍在（route 本質就是把 model name 切成 family）
    - 但 `route.Resolve` 改成回 `provider.Provider`，不再回 `upstream.Profile`

### Phase D：驗證

16. **跑** `cd agentsdk && go build ./...`
17. **跑** `cd proxy && go build ./...`
18. **跑** `cd proxy && go test ./...`（確認 handler_test、middleware_test、observability_test 仍綠）
19. **建立** `providers/<name>/<name>_test.go`：測 `dto.Validate()` + `auth_api.go` env fallback + `stream.go` 給假 SSE feed 確認 chunk 折疊順序
20. **檢查**：每個 provider module 仍只用自己需要的外部依賴（anthropic 用 sdk-go；grok/ollama/antigravity/codex 用 stdlib；無 SDK 的禁止引外部大包）
21. **不要 git commit** — 等 user 看完 diff 再決定

---

## 影響範圍

| 維度 | 數字 |
|---|---|
| 新 module 數 | +5（anthropic / ollama / grok / antigravity / codex）— google 移除，openaicompat 改名 ollama |
| 刪除檔案 | 約 15~20 檔（proxy/provider 整層 + transform 大部分） |
| 修改檔案 | `agentsdk/core/port.go`、`agentsdk/core/input.go`、`proxy/svc/upstream/client.go`、`proxy/svc/upstream/{dispatcher.go NEW}`、`proxy/handlers/handler.go`、`proxy/svc/route/`、`proxy/svc/transform/registry.go` |
| 新增行數估計 | 5 個 provider × 約 600~800 行/個 = 3000~4000 行 |
| 刪除行數估計 | proxy Profile/Catalog/transform 9 對約 3000~5000 行 |
| 淨變動 | 程式碼總量持平或略減；介面數從 3 個（anthropic/google/openaicompat）變 5 個 |

---

## 驗證方式

```bash
# 1. agentsdk 各 module 編譯
cd /Users/shuk/projects/agentSDK
for m in provider/anthropic provider/ollama provider/grok provider/antigravity provider/codex; do
  (cd "$m" && go build ./... && go test ./...)
done

# 2. root 編譯
go build ./... && go test ./...

# 3. proxy 編譯 + 測試
cd /Users/shuk/projects/ai/proxy
go build ./... && go test ./...

# 4. wire-format smoke
# 對每個 provider：發假 request → dispatcher → 期望收到正確 stream chunks
# 這部分以 handler_test.go + 新增 providers/<n>/stream_test.go 涵蓋
```

**最終 review 條件**
- `git status` 乾淨（untracked 新檔 + 修改檔列出來給 user 看）
- `go build` 全綠
- `go test` 全綠
- user 同意才 commit

---

## 不在這次範圍（之後再說）

- `auth/store.go` credential persistence（0700/0600 檔案鎖、atomic temp+rename）— 之後讓 provider 用一個共通 store
- OAuth device flow 的瀏覽器自動開啟 — 之後
- TUI 用 provider.Models() 做 picker
- Antigravity 的 wire-format catalog（先 placeholder + TODO，等實測抓 packet）
- proxy 的 3 種 client wire format × 5 個 provider 的 transform 完整覆蓋 — 先保留 9 對結構，把邏輯搬到 provider 內
