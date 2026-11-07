# proxy — 技術脈絡 (Technical Context)

## 專案結構 (Project Structure)

```tree
proxy/
├── main.go                       # cobra entry; 載 gosdk/log、執行 ProxyCmd
├── go.mod / go.sum               # module github.com/bizshuk/proxy (go 1.26.0)
├── package.json                  # npm scripts 統一執行 Go build/test/vet/verify
├── ecosystem.config.js           # pm2 部署 (namespace: Service)
├── settings.example.json         # 範例設定 (host/port/auth-dir/api-keys/timeouts/...)
├── scripts/                      # *.http 範例 + realtime-webrtc.html browser client
├── cmd/
│   ├── proxy.go                  # cobra ProxyCmd; 預設 --port 8317
│   ├── options.go                # proxy options/models; Codex + OpenAI/Grok image model catalog
│   └── image_mcp.go              # proxy image-mcp; stdio MCP entry
├── config/
│   └── config.go                 # Config struct + LoadConfig (gosdk viper, APP_NAME="agentSDK")
├── mcpimage/                     # Codex / Claude Code 共用圖片 MCP server
│   ├── config.go                 # base URL / port / API key / model / output 設定
│   ├── client.go                 # POST /v1/images/generations + Base64 解碼
│   ├── tool.go                   # generate_image + 專案內檔案輸出
│   └── server.go                 # official Go SDK stdio MCP server
├── plugins/proxy-imagegen/       # Codex + Claude Code plugin manifests / imagine skill
├── .agents/plugins/              # Codex local marketplace
├── .claude-plugin/               # Claude Code local marketplace
├── handlers/                     # gin engine + 全部 HTTP 表面
│   ├── server.go                 # Server.New: 組 engine + middleware + 路由表
│   ├── handler.go                # Handler.Handle / HandleModels / HandleCountTokens
│   ├── image_generation.go       # /v1/images/generations OpenAI-compatible JSON pass-through
│   ├── image_edit.go             # /v1/images/edits multipart image-edit pass-through
│   ├── middleware.go             # requireAPIKey / corsLocalhost / rateLimitPerIP
│   ├── observability.go          # TransformObserver (OTel counters + slog)
│   ├── codex_log.go              # Codex OAuth 請求脫敏 metadata log
│   ├── realtime.go               # OpenAI Realtime WebSocket/WebRTC transport proxy
│   └── upstream_error_log.go     # 4xx/5xx 與 stream 終止結構化日誌
├── model/                        # 跨層共用 wire model
│   ├── format.go                 # Format enum (anthropic-messages/openai-chat/openai-responses)
│   ├── envelope.go               # RequestEnvelope / ResponseEnvelope / Exchange / TransformResult
│   ├── error.go                  # ProxyError + EncodeError (依 format 編碼)
│   └── sse.go                    # agentsdk provider/protocol/sse compatibility facade
├── model/
│   ├── anthropic/types.go        # Anthropic Messages wire DTO
│   ├── chat/types.go             # OpenAI Chat Completions wire DTO
│   ├── responses/types.go        # OpenAI Responses wire DTO
│   └── antigravity/types.go      # agentsdk wire type 的 alias + 兩個加寬容器
├── svc/
│   ├── route/                    # 模型名 → provider family 解析
│   │   ├── router.go             # Router.Resolve (qualifier / exact / prefix)
│   │   └── profile.go            # route.Profile (routing 鍵)
│   ├── transform/                # client 3×3 完整矩陣 + provider-only 目標
│   │   ├── registry.go           # Registry (client 完整性 + provider-only 可達性驗證)
│   │   ├── default.go            # NewDefaultRegistry (9 對 client + antigravity 目標)
│   │   ├── identity.go           # 三個 client format 的 identity pair
│   │   ├── response.go           # DecodeUpstreamError + 共用 helper
│   │   ├── collector.go          # StreamCollector (anthropic/chat/responses)
│   │   ├── types.go              # Pair / RequestTransform / ResponseTransform / StreamTransform
│   │   ├── anthropic_chat_{request,response,stream}.go
│   │   ├── anthropic_responses_{request,response,stream}.go
│   │   ├── anthropic_antigravity_{request,response,stream}.go
│   │   ├── antigravity_signature.go  # tool thought-signature 跨請求 replay cache
│   │   └── chat_responses_{request,response,stream}.go
│   └── upstream/                 # 上游 profile + dispatcher + transport
│       ├── profile.go            # Profile / Catalog / DefaultCatalog + normalize*
│       ├── dispatcher.go         # Dispatcher (live provider.Adapter 集合，key 為 agentsdk registry name)
│       ├── dispatcher_default.go # NewDefaultDispatcher (走 provider.Names() + env-var)
│       ├── dispatcher_oauth.go   # NewDispatcherWithAuth[AndEnv] (走 agentsdk credential.Source)
│       ├── credential.go         # CredentialResolver (包 auth/svc.Resolver)
│       ├── client.go             # Client.Do / CountTokens / GenerateImage / EditImage + header/secret 套用
│       ├── identity.go           # ApplyIdentityHeaders hooks: 各 provider 的 client identity header
│       ├── antigravity.go        # ApplyCredentialBody hook: 把憑證的 project 蓋進 envelope
│       ├── realtime.go           # 固定 Realtime endpoints + API-key target/header
│       └── config.go             # TimeoutConfig
└── docs/
    ├── terminology.md            # 領域術語的單一定義來源
    ├── plans/                    # 進行中計畫
    └── specs/                    # 設計規格 (含 legacy 已被取代)
```

## 技術棧 (Tech Stack)

- Language: Go 1.26.0
- HTTP framework: `github.com/gin-gonic/gin` v1.11.0
- CLI: `github.com/spf13/cobra` v1.10.2 + `github.com/spf13/viper` v1.21.0
- Config / logging / middleware: `github.com/bizshuk/gosdk` v1.3.5
- Auth: `github.com/bizshuk/auth` v0.0.2 (FileStore + svc.Resolver + provider.For)
- Provider SDK: `github.com/bizshuk/agentsdk` v0.0.49 (core + provider/\* + protocol/sse + antigravity 協定)
- MCP SDK: `github.com/modelcontextprotocol/go-sdk` v1.6.0
- Observability: `log/slog` (stdlib) + `go.opentelemetry.io/otel` v1.44.0
- Test: `github.com/stretchr/testify` v1.11.1

## 關鍵決策 (Key Decisions)

- 自行構造 `gin.Engine` 而非用 `gosdk/server.Run`：因為後者持有自己的 engine、不開放 route hook；middleware 仍共用 `gosdk/mw.CorrelationID` / `mw.Helmet`。
- 三格式 (`anthropic-messages` / `openai-chat` / `openai-responses`) 用 sealed string type `model.Format` 而非 iota enum：proto wire shape 在三方 SDK 之間無單一真理來源，string literal 反而最能避免序列化漂移。
- `model.Format` 分成 `CLIENT_FORMATS` 與 `PROVIDER_FORMATS`：client format 是 proxy 對外收也對外送的協定，provider format (目前只有 `antigravity`) 只能當 transform 目標。`transform.Registry` 因此只對 client format 做 complete-matrix 檢查 (缺一對就 error)，另外要求每個 provider format 至少有一個 client 能到達、且永不出現在 pair 的 `From` 側。
- Antigravity 是 provider-only format：wire 是 Gemini `generateContent` 外包一層 Cloud Code envelope (`model` / `project` / `userAgent` / `requestType` / `requestId` / `request`)，與公開 Gemini API 完全不同的 endpoint 與語意，所以不併入 `google` profile。目前只有 `anthropic-messages` 能到達；`openai-chat` / `openai-responses` 打過來會拿到明確的 unsupported-pair 錯誤，而不是被降級轉譯。
- Antigravity endpoint 分流：非串流走 `/v1internal:generateContent`，串流走 `/v1internal:streamGenerateContent?alt=sse` (不帶 `alt=sse` 會回 chunked JSON 而非 SSE)。`Profile.StreamEndpoints` 就是為這種「串流與非串流不同路徑」而存在；`buildEndpointURL` 因此放寬為允許 profile 常數自帶固定 query，但仍禁止 fragment。
- `Profile.ApplyCredentialBody` 是 `NormalizeRequest` 的`憑證版孿生 hook`：後者在憑證解析`前`執行，看不到憑證，所以「wire format 把帳號狀態放在 body 而非 header」的 provider 無處落腳。Antigravity 是目前唯一需要的 (envelope 的 `project`)，但 `client.go` 因此不再有 `if profile.ID == ANTIGRAVITY` 的硬編碼特判 —— transport 只問 hook 是不是 nil。
- `svc/upstream/antigravity.go` 只剩填這個 hook 的實作 (約 50 行)。先前的 project 查詢與快取已隨 auth 接手而刪除。
- Antigravity tool schema 的 wire 欄位是 `parameters` 而非 `parametersJsonSchema` (gateway 以 protobuf Schema 驗證)；claude-\* 模型帶 tools 時必須補 `toolConfig.functionCallingConfig.mode = VALIDATED`。三份獨立實作 (CLIProxyAPI、antigravity-claude-proxy、antigravity-proxy) 對這兩點一致。
- Antigravity tool loop 的 thought signature 必須跨請求還原：Gemini 3 會拒絕任何缺 `thoughtSignature` 的重播 functionCall，但 Anthropic Messages 的 `tool_use` block 沒有這個欄位。proxy 兩手都做 —— response/stream 把 signature 寫在 block 上 (保留未知欄位的 client 直接可用)，同時以 tool_use ID 為鍵存進 `antigravitySignatureCache` (bounded 4096, FIFO)，request 端在 block 沒帶 signature 時查回。key 選 tool_use ID 是因為它由 gateway 產生、原樣經 client 折返，即使 client 丟欄位也不會變。這是 transform 層唯一的跨請求狀態：producer 與 consumer 是不同請求，放不進 request-scoped `Exchange`；而 `svc/upstream` 看不到 decode 後的 response。
- Antigravity 的`協定`歸 `agentsdk/provider/antigravity` 所有: endpoint、client identity header、host fallback、project discovery、Cloud Code envelope 與 Google-dialect schema 轉換都在那邊，`model/antigravity` 只是 alias。proxy 只保留 `wire-to-wire` 的 transform —— agentsdk 的 encode/decode 走 `core.ModelRequest`，那是最小公分母抽象，帶不動 Anthropic 的 thinking signature / cache_control / 多模態 tool result。
- `model/antigravity` 仍自行宣告 `GenerationConfig` 與 `Inner` 兩個容器: `core.ModelRequest` 完全沒有 sampling 欄位, 所以 agentsdk 的 `GenerationConfig` 沒有 `temperature` / `topP`，其 `GenerateRequest` 也放不下有這兩欄的型別。gateway 是收的 (每個參考 client 都送)，靜靜丟掉會違背 Anthropic caller 的指定，因此只加寬這兩個容器，leaf type 全部沿用 agentsdk。
- Tool schema 轉換委由 `ag.CleanSchema`: 它把 type 轉大寫、collapse union、過濾 unsupported keyword。proxy 只在呼叫前補一個正規化 —— `{"type":"object"}` 這種完全沒有 `properties` 的零參數 tool，agentsdk 的 placeholder 規則不會觸發 (它只認 `properties: {}`)，而 Google 兩種都拒。
- `Provider wire fact` 一律不在 proxy 宣告：base URL、endpoint path、identity header、client version、預設 model 都屬於該 vendor 的 `agentsdk/provider/*` package，`svc/upstream` 只引用 (`sdkanthropic.*` / `sdkcodex.*` / `sdkgrok.*` / `ag.*`)。判準是「換一支 client 打同一個 gateway 也一樣」。唯一豁免是 `api.openai.com` —— agentsdk 只模型化 Codex OAuth endpoint，沒有 `provider/openai` adapter，所以那組 `OPENAI_*` 常數暫留 proxy。`svc/upstream/boundary_test.go` 把這條規則變成會失敗的測試。
- `Profile.ApplyIdentityHeaders` 是 `ApplyCredentialBody` 的 header 版孿生：gateway 在憑證之外還檢查的「誰在呼叫」(Codex 的 originator/version、Antigravity 的 IDE identity、Grok OAuth 的 CLI identity 與 tracking id) 由 profile 自帶 hook，`client.go` 只問 hook 是不是 nil。因此 transport 內已無任何 `if profile.ID == ...` 分支。`Surface` 參數存在的理由是 xAI：Imagine 走公開 host，會拒收 inference 用的 cli-chat-proxy identity header。
- `Profile.OAuthScheme` 讓憑證種類影響 auth header 成為`資料`而非分支：Anthropic 同一 endpoint 對 API key 讀 `x-api-key`、對 access token 讀 `Authorization`。空值表示兩者相同。原本的 `AnthropicVersion` 欄位已刪除 (只有一個 profile 用)，改由該 profile 的 identity hook 發送。
- `CLIENT_IDENTIFIER = "proxy"` 是本程式對 gateway 的自稱，刻意不冒用參考 CLI 的名字 —— gateway 依此歸因流量，冒名會錯報呼叫者。這是少數留在 `svc/upstream` 的字串常數之一。
- Dispatcher 與 Catalog 並存：Dispatcher 持有 live `core.Provider`，供 `/v1/models` 取 catalog；Catalog 持有每家 profile 的 endpoint/auth header/normalizer，供 `Client.do` 使用。短期不收斂 (見 `docs/specs/2026-07-16-pairwise-agent-provider-transform.md` 之 Phase C 註記)。
- `Pair.NewStream` 採「factory 產生 isolated `StreamTransform`」模型：每個請求一份狀態，無共享 mutex；與 `StreamCollector` 的 `Push`/`Close` 模型對稱。
- `handleBridge`：當 client 要求非串流但 `Profile.NormalizeRequest` 標記 `BridgeToNonStream=true` (例如 Codex OAuth 強制 `stream:true`) 時，把上游 SSE 流整段收集後回 JSON；`boundedStreamCollector` 防止記憶體爆。
- `extractAPIKey` 同時支援 `x-api-key` 與 `Authorization: Bearer`，匹配走 `subtle.ConstantTimeCompare` 防 timing oracle；map membership 本身會洩漏，range loop 才是常數時間。
- `corsLocalhost` 鎖定 `localhost` / `127.0.0.1` origin：proxy 預期跑在開發者本機或機房閘道後方，不對外網開瀏覽器 CORS。
- `WriteTimeout: 0`：SSE 串流不可被固定 deadline 切斷；由 client 端的 `request.Context()` cancel 觸發 graceful shutdown。
- `MAX_UPSTREAM_ERROR_BYTES = 64<<10`：日誌最多印 64 KiB 上游錯誤 body；超出部分以 `body_truncated: true` 標記 + `body_bytes` 計數。
- `sensitiveHeaders` deny-list：`authorization` / `proxy-authorization` / `cookie` / `set-cookie` / `x-api-key` / `api-key` / `x-auth-token` / `x-amz-security-token` 等 8 個 key 永不寫進日誌。
- Codex OAuth payload 脫敏：只印 `model` / `stream` / `store` / `instructions_bytes` / `has_instructions` / `input_roles` / `tool_names` / `parallel_tool_calls`，原始 `instructions` 與 `input[].content` 絕不進 slog。
- xAI credential kind 決定 inference profile：API key 選 `xai` (`https://api.x.ai`)，OAuth 選 `xai-grok-oauth` (`https://cli-chat-proxy.grok.com/v1`)；兩者共享 `xai` routing family，OAuth token 不得誤送公開 inference API。
- `xai-grok-oauth` 對齊 `grok-build` 的三種 inference protocol：`/responses`、`/chat/completions`、`/messages`。`xai-responses` / `xai-chat` / `xai-messages` qualifier 強制 target format；無 format qualifier 時偏好 Responses。
- Grok OAuth request 固定由 `Client.do` 注入 bearer、`X-XAI-Token-Auth`、`x-authenticateresponse`、`x-grok-client-*` 與 request metadata；downstream 只能傳 conversation/session/turn/agent/deployment tracking 欄位，不能覆寫 auth、client identity、routed model 或 credential user identity。
- Imagine 不屬於 inference profile：`Client.GenerateImage` 依 profile 直連 OpenAI `https://api.openai.com/v1/images/generations` 或 xAI `https://api.x.ai/v1/images/generations`；xAI OAuth bearer 具 `api:access` scope，OpenAI OAuth 仍使用 Codex profile 且不提供圖片 endpoint。request/response JSON 不進 `model.Format` 或 transform matrix。
- Image Edit 是獨立的 OpenAI-compatible multipart surface：`POST /v1/images/edits` 由 `HandleImageEdits` 驗證 `model` / `prompt` / `image`，保留原始 multipart body，並由 `Client.EditImage` 以 `openai-api` profile 轉送到 `https://api.openai.com/v1/images/edits`；不把 uploaded image 寫入 log 或 transform matrix。
- image generation 使用獨立 HTTP client，總 timeout `300s`、response-header timeout `240s`，避免 Imagine 在 server-side buffer 圖片期間被一般 messages timeout 切斷。
- `proxy image-mcp` 是 client-side `stdio MCP` adapter：以 `PROXY_IMAGE_BASE_URL` / `PROXY_IMAGE_PORT` / `PROXY_IMAGE_API_KEY` 連回本 proxy，固定要求 `b64_json`，驗證實際圖片 MIME 後同時回 MCP image content 與專案內檔案路徑。
- Codex 與 Claude Code 共用 `mcpimage` server，不共用 manifest：Codex manifest 以 `env_vars` 轉送 parent environment；Claude Code `.mcp.json` 以 `${user_config.*}` 注入安裝設定。Plugin 只依賴 PATH 中的 `proxy` binary，不複製或內嵌另一份 transport 實作。
- 相對圖片輸出目錄先經 traversal 檢查，再以 MCP `roots/list`、`CLAUDE_PROJECT_DIR`、working directory 的順序定位 project root；路徑解析與目錄建立在付費圖片 API 呼叫前完成。
- Grok response metadata (`x-grok-context-window` / `x-grok-max-completion-tokens` / `x-models-etag` / `x-should-retry`) 列入 profile allowlist；成功走 `copySafeResponseHeaders`，4xx/5xx 走會移除 upstream content type 的 `copySafeErrorResponseHeaders`。
- `normalizeXAIGrokOAuthRequest` 保留 xAI-specific raw tools；Responses 預設 `store:false` + `reasoning.encrypted_content`，streaming Chat 合併 `stream_options.include_usage:true`，Messages 缺少 `max_tokens` 時用 `128000`。
- Anthropic `thinking` 與 Responses typed `reasoning` 必須對稱：v2 `thinking.signature` 保存 `id` / exact `summary` / optional `content` / `encrypted_content` / `status`，decoder 保持 v1 (`id` / `encrypted_content`) compatibility；SSE 在 reasoning item 完成時送 `signature_delta`，非本 proxy 產生的 Anthropic signature 只記 semantic loss，不冒充 Responses encrypted state。
- SSE framing 由 agentsdk `provider/protocol/sse` 單一擁有；`model/sse.go` 只保留 compatibility aliases / wrappers。共用 decoder 只在 stream 開頭移除 UTF-8 BOM，provider / format terminal semantics 仍留在個別 transform。
- auth storage provider ID 與 Agents SDK provider ID 不同名 (`xai` vs `grok`、`openai` vs `codex`)，這個邊界映射由 agentsdk 的 `provider/credential` 擁有 (`credential.RouteID` / `credential.Names`)，proxy 不再自備路由表。舊碼的 `familiesInDefaultOrder` 直接向 resolver 查 `codex`，但 auth 從不以該名存憑證，故 codex 憑證實際上永遠只能從 env fallback 進來；改用 `credential.Names()` 後修正。
- `core.Provider` 是純能力介面 (`Generate` / `Stream`)，不帶 `ID()` / `Models()`：名字屬於索引 adapter 的 registry，不屬於 adapter 自身。因此 `Dispatcher.Set(name, adapter)` 由呼叫端給名，`AdvertisedModels` 走 `provider.Catalog(name)`（免憑證、免 I/O）。
- 憑證不再於建構時快照：`credential.Source.Decorator()` 每次呼叫重新向 auth store 解析，token 輪替不需 `Dispatcher.Replace`。startup 仍探測一次，讓沒有憑證的 family 不出現在 `/v1/models`。
- OpenAI Realtime 是獨立 transport，不加入 `model.ALL_FORMATS`：`/v1/realtime` 透過 WebSocket 雙向 tunnel；`/v1/realtime/calls` 與 `/v1/realtime/client_secrets` 代理 WebRTC 初始化。三者保留 OpenAI GA event schema，不做 pairwise transform。
- Realtime 只接受標準 OpenAI API-key credential，不使用 ChatGPT Codex OAuth；downstream authorization 永不透傳，音訊與 event payload 永不寫入 log。

## 模組對應 (Module Mapping)

| 業務領域 (Domain)                       | 套件/模組 (Package/Module)                                                                | 進入點 (Entry Point)                                                              |
| --------------------------------------- | ----------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| 協定轉譯 (Protocol Translation)         | `svc/transform`, `model/anthropic`, `model/chat`, `model/responses`                       | `transform.NewDefaultRegistry()` → `Registry.Lookup`                              |
| 模型路由 (Model Routing)                | `svc/route`, `svc/upstream` (`ResolveProfile` / `NewRouter`)                              | `Router.Resolve(format, modelName)`                                               |
| 憑證解析 (Credential Resolution)        | `svc/upstream` (`CredentialResolver`), `agentsdk/provider/credential`, `bizshuk/auth/svc` | `CredentialResolver.Resolve` (request path) / `credential.NewAutoSource` (wiring) |
| 上游調度 (Upstream Dispatch)            | `svc/upstream` (`Profile` / `Catalog` / `Dispatcher` / `Client`)                          | `DefaultCatalog()` + `Client.Do`                                                  |
| HTTP 公開介面 (HTTP Surface)            | `handlers` (`Server` / `middleware` / `observability`)                                    | `Server.New(cfg).Run(ctx)`                                                        |
| 請求生命週期 (Request Lifecycle)        | `handlers/handler.go`, `handlers/codex_log.go`, `handlers/upstream_error_log.go`          | `Handler.Handle(format)`, `Handler.HandleModels`                                  |
| 圖片生成 (Image Generation)             | `handlers/image_generation.go`, `svc/upstream/client.go`, `svc/upstream/profile.go`       | `Handler.HandleImageGenerations` → `Client.GenerateImage`                         |
| 圖片編輯 (Image Edit)                   | `handlers/image_edit.go`, `svc/upstream/client.go`, `svc/upstream/profile.go`              | `Handler.HandleImageEdits` → `Client.EditImage`                                    |
| 圖片 MCP 接入 (Image MCP Integration)   | `mcpimage`, `cmd/image_mcp.go`, `plugins/proxy-imagegen`                                  | `proxy image-mcp` → `generate_image`                                              |
| Antigravity 接入 (Antigravity Access)   | `model/antigravity`, `svc/transform/anthropic_antigravity_*.go`, `svc/upstream/antigravity.go` | `AnthropicToAntigravityRequest` → `Client.Do` (profile `antigravity`)         |
| 即時語音傳輸 (Realtime Voice Transport) | `handlers/realtime.go`, `svc/upstream/realtime.go`                                        | `RealtimeHandler.HandleWebSocket`, `HandleHandshake`                              |
| 設定與生命週期 (Config & Lifecycle)     | `config`, `cmd`, `main`, `ecosystem.config.js`                                            | `cmd.ProxyCmd.RunE`                                                               |

## 觀測鏈 (Observability Chain)

`LOG_LEVEL=debug` 時 proxy 沿 request lifecycle 發出 4-5 個 `proxy debug payload` structured log records:

| Stage         | 觸發時機                              | 內容                                          |
| ------------- | ------------------------------------- | --------------------------------------------- |
| `req.before`  | client body 讀完 + metadata parse 後  | 原始 client body (≤ 64 KiB)                   |
| `req.now`     | transform + NormalizeRequest 都成功後 | 送上游的 wire body                            |
| `resp.before` | upstream 4xx/5xx 時                   | upstream 錯誤 body                            |
| `resp.now`    | 2xx + non-stream 成功路徑             | response transform 後的 body                  |
| `req.failed`  | 任何內部步驟失敗時                    | `error_code` / `error_kind` / `error_message` |

完整鏈路圖、success/failure sequence diagram、stage 對照表：

📄 [`docs/specs/2026-07-22-debug-payload-chain.md`](specs/2026-07-22-debug-payload-chain.md)

實作在 `handlers/debug_log.go` (`emitDebugPayload` / `emitDebugFailure` / `truncateBytes`)，觸發點散佈於 `handlers/handler.go` (Handle + HandleCountTokens 共 9 個 emit 點)。

## 開發指南 (Development Guide)

### 前置需求 (Prerequisites)

- Go 1.26.0
- Node.js / npm（只作為統一開發命令入口，無 runtime dependency）
- 已安裝 `bizshuk/gosdk`、`bizshuk/auth`、`bizshuk/agentsdk` (透過 go.mod 自動取得)
- 本機預期有 `~/.config/agentSDK/settings.local.json` (首次跑 `proxy config init` 會建立)

### 安裝 (Installation)

```bash
go mod download
```

### 建置 (Build)

```bash
npm run build
```

### 測試 (Test)

```bash
npm test
```

(整個專案大量使用 `_test.go`：handlers/ 有 10 個、svc/transform/ 有 13 個、svc/route 有 1 個、svc/upstream 有 5 個、model 有 5 個、mcpimage/ 有 5 個。)

### 靜態檢查與完整驗證 (Static Analysis and Verification)

```bash
npm run vet
npm run verify
```

`npm run verify` 依序執行完整 Go test、build 與 vet；實際 Go 命令集中定義於 `package.json`。

### 部署 (Deploy)

- 單機：`go build && ./proxy --port 8317`
- pm2 (見 `ecosystem.config.js`)：`pm2 start ecosystem.config.js` (namespace `Service`、instances 1)

設定位於 `~/.config/agentSDK/settings.json` 與 `settings.local.json`；CLI `proxy config get|set` (由 `gosdk/cmd.ConfigCmd` 提供) 可即時讀寫。

## 慣例 (Conventions)

- Naming:
    - `model.Format` 用小寫 hyphenated 字串 (`"openai-chat"`、`"anthropic-messages"`、`"openai-responses"`)
    - 常數使用 `SCREAMING_SNAKE_CASE` (例如 `MAX_UPSTREAM_ERROR_BYTES`、`ANTHROPIC_OAUTH_BETA`)
    - Profile ID 一律小寫無空白 (例如 `anthropic`、`openai-codex-oauth`、`xai-grok-oauth`、`minimax`)
- Error handling:
    - 內部錯誤統一用 `*model.ProxyError` (Kind/Status/Code/Message/Cause)，`writeError` 依 `format` 編碼回來源格式
    - `Kind` 透過 `StatusCode()` 取得 HTTP status，並透過 `publicErrorType` 映射為 wire error type (`invalid_request_error` / `authentication_error` / `rate_limit_error` / `api_error`)
    - `Cause` 透過 `Unwrap()` 暴露給 `errors.As/Is`
- Logging:
    - 一律 `log/slog` (`_ "github.com/bizshuk/gosdk/log"` 在 main.go 設定 default logger)
    - 結構化 attrs：必含 `request_id` (handler 自己用 `requestID()` 從 `x-request-id` 或新 uuid 補上)、`provider`、`model`、`source_format`、`target_format`
    - 4xx/5xx 上游錯誤走 `Error` 級 + `logUpstreamError` 過濾敏感 header
- Testing:
    - 全部使用 `testify/assert` 與 `testify/require`
    - `model.ALL_FORMATS` 在 `Registry.NewRegistry` 內被列舉驗證；測試通常用 `NewDefaultRegistry()` 拿到 production matrix
    - SSE 測試透過 `model.NewBoundedSSEDecoder` 與 `model.WriteSSE` compatibility facade 構造 round-trip，並驗證其型別與錯誤直接對應 agentsdk `provider/protocol/sse`
- Streaming:
    - `StreamTransform.Push(ctx, frame)` 與 `Close(ctx)` 都接收 ctx，便於 client 取消時及時釋放
    - `WriteTimeout: 0` 在 `newHTTPServer` 內強制設定；client cancel 走 graceful shutdown
- 設定覆寫:
    - dot-form 鍵 (例如 `server.port`、`body-limit-mb` 在 mapstructure 但 `timeouts.messages-ms` 在 mapstructure 但帶 dash) → `settings.json` + `settings.local.json`
    - 無 dash 的 dot-form 鍵可被 `APP_SERVER_PORT` 等 env 變數覆寫 (gosdk viper 預設)
    - 帶 dash 的鍵 (例如 `auth-dir`、`body-limit-mb`、`timeouts.*-ms`) 是 file-only
    - Realtime 限制 (`realtime.max-connections`、`realtime.max-handshake-bytes`) 同樣是 file-only；`realtime.enabled` 可由 `APP_REALTIME_ENABLED` 覆寫
