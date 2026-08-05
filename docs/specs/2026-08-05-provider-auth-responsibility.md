# Provider 與 Auth 責任邊界 (Provider and Auth Responsibility Boundary)

2026-08-05

## 目的 (Purpose)

界定 proxy 該擁有什麼、不該擁有什麼, 並把不屬於 proxy 的知識推回它真正的擁有者。
結論一句話: proxy 只做 `wire format 之間的轉譯` 與 `基本網路協定存取`; 其餘一律外包。

## 三方擁有者 (The Three Owners)

| 擁有者                 | 擁有什麼                                                                       | 判準                                       |
| ---------------------- | ------------------------------------------------------------------------------ | ------------------------------------------ |
| `agentsdk/provider/*`  | Vendor wire facts: base URL, endpoint path, identity header, client version, 預設 model | 「換一個 client 打同一個 gateway 也一樣」  |
| `bizshuk/auth`         | Credential 語意: 選擇、refresh、持久化、account/project provisioning            | 「換一個 gateway 打同一個帳號也一樣」      |
| `proxy`                | Format transform、HTTP transport、catalog key、本程式身分                       | 「只有這支 proxy 這樣做」                  |

## 已完成的下放 (Completed Shifts)

### 到 agentsdk

`provider/anthropic/config.go` 新增 `DefaultBaseURL`、`APIVersionHeader`/`APIVersion`、
`DirectBrowserAccessHeader`/`Value`、`PATH_MESSAGES`/`PATH_COUNT_TOKENS`;
`provider.go` 內原本硬編的 `2023-06-01` 與 messages URL 改引用之。

`provider/codex/config.go` 新增 `PATH_RESPONSES`、`OriginatorHeader`/`VersionHeader`/`AccountIDHeader`,
並把 `CodexVersion` 由 `0.125.0` 升到 `0.144.1` (舊值會讓 `gpt-5.6-sol` 回 400 —— 這是 agentsdk 自身的 bug, 不只是 proxy 的 override)。

`provider/grok/config.go` 補上先前完全缺席的 `OAuth flavor`: `OAuthBaseURL`、三組
identity header、`ClientVersion`、`DefaultClientMode`、`DefaultMaxTokens`、
tracking / response metadata header 常數、`UserAgent(identifier)`;
並把 `defaultImageModel` 匯出為 `DefaultImageModel`。

`provider/utils/useragent.go` 新增 `CLIUserAgent(identifier, version)` —— codex 與 grok
共用的 `name/version (platform; arch)` 格式, 兩處各自硬編的 GOOS/GOARCH 對照表收斂為一份。

### 在 proxy 內部

`svc/upstream/identity.go` (新檔) 引入 `ApplyIdentityHeaders` hook 與 `Surface` 概念。
`client.go` 因此不再有任何 `if profile.ID == ...` / `EqualFold(CredentialProvider, "xai")`
分支 —— transport 只問 hook 是不是 nil, 與既有的 `ApplyCredentialBody` 對稱。

`Profile.AnthropicVersion` 欄位刪除 (只有一個 profile 用), 改由 anthropic 的 identity hook 發送。
`Profile.OAuthScheme` 新增: Anthropic 同一 endpoint 對 API key 讀 `x-api-key`、對 access token
讀 `Authorization`, 這從 transport 的 if 分支變成 profile 上的資料。

`svc/upstream/boundary_test.go` (新檔) 把上述邊界變成`會失敗的測試`: 任何人在
`svc/upstream` 重新宣告 vendor base URL / path / identity header 都會被擋下。

## 刻意留在 proxy 的東西 (Deliberately Kept)

- `XAI_GROK_OAUTH_PROFILE_ID` / `ANTIGRAVITY_PROFILE_ID` —— catalog row 的鍵, 不是 wire 上的東西。
- `CLIENT_IDENTIFIER = "proxy"` —— 本程式對 gateway 的自稱。冒用參考 CLI 的名字會錯報呼叫者。
- `OPENAI_*` base URL 與 path —— agentsdk 只模型化 Codex OAuth endpoint, 沒有 `api.openai.com` adapter。
  等 `provider/openai` 出現的那天再搬。
- `OPENAI_IMAGE_DEFAULT_MODEL = "gpt-image-2"` —— 同上, 沒有上游擁有者可以委託。
- `RESPONSES_ENCRYPTED_REASONING` —— Responses 協定的 `include` 值, proxy 自己在 `model/responses` 擁有該協定。
- `normalizeCodexRequest` / `normalizeXAIGrokOAuthRequest` / `normalizeXAIRequest` ——
  這些`操作 proxy 自己的 DTO`。要求本身 (Codex 拒收 `max_output_tokens`) 是 provider fact,
  但改寫動作只能發生在懂這些 DTO 的一層。常數已下放 (`sdkgrok.DefaultMaxTokens`), 邏輯留下。
- `Catalog.ResolveProfile` 的 credential-kind switch —— 它選的是 proxy catalog 的列, 不是 auth 的 route id;
  後者由 `credential.RouteID` 擁有且 proxy 不再自備。

## 尚未處理 (Remaining)

1. `Profile.AdvertisedModels` 與 `route.Profile` 的 prefix / exact model 清單, 與
   agentsdk `provider.Catalog(name)` 的內容重疊。收斂會改動 `/v1/models` 輸出與路由語意, 風險不小, 未動。
2. `handlers.imageProviderFamily` 用 `gpt-image-` / `dall-e-` prefix 判斷家族並`預設落到 xai`,
   完全繞過 `svc/route.Router`。這是 proxy 內部的一致性問題, 不是責任邊界問題。
3. `Client.GenerateImage` / `EditImage` 對 OAuth credential 清掉 `BaseURL`。
   理由 (OAuth token 的 image scope 打的是公開 host) 現已記在 `sdkgrok` 的註解, 但這個決定本身仍在 proxy。

## 落地前置 (Landing Prerequisite)

agentsdk 側的變更尚未發版。proxy 目前透過本機 `go.work` 消費, 需要:

1. agentsdk commit + tag (例如 `v0.0.50`)
2. proxy `go.mod` 升到該版
3. 刪除 proxy 的 `go.work` / `go.work.sum` (已在 `.gitignore` 內)
