# Client / Provider Wire Format Catalog

`結論`：四個指定來源合計可證明 `37` 個 client/provider wire-format entity。目錄以 `<client>__<provider>` 命名；每個 entity 同時記錄 client send、provider receive、provider send 與 client receive。

## 快照

| 代碼 | Source | Commit/branch | 主要證據 |
| --- | --- | --- | --- |
| `A2` | `auth2api` | `main@37c9b864` | [handlers/providers](../../../../../tmp/auth2api/src/providers/types.ts)、[Cursor wire](../../../../../tmp/auth2api/src/upstream/cursor-api.ts) |
| `CP` | `CLIProxyAPI` | `main@411d7d41` | [translator formats](../../../../../tmp/CLIProxyAPI/sdk/translator/formats.go)、[registrations](../../../../../tmp/CLIProxyAPI/internal/translator/init.go) |
| `AM` | `agentSDK` | `master@e7edfc7c` | `git show e7edfc7c:proxy/adaptor/adaptor.go` |
| `AC` | `agentSDK` | `feature/pairwise-provider-transform@39a913cb` | [3×3 registry](../../../svc/transform/default.go)、[provider profiles](../../../svc/upstream/profile.go) |

快照只代表本次盤點的 local commit；未執行 remote fetch。`agentSDK current` 的 proxy source 沒有 working-tree 修改。

## Entity 定義

- `client`：proxy 公開端點接收與回傳的 wire format。
- `provider`：proxy 實際送往 upstream 並從 upstream 接收的 wire format。
- `entity`：一個有程式碼證據的 directed pair；vendor/profile 若重用相同 provider format，不建立重複目錄。
- JSON 範例是 source-evidenced payload subset，不是 vendor 全量 OpenAPI/JSON Schema。

## Client aliases

| Client format | 常見 client |
| --- | --- |
| `anthropic-messages` | Claude Code、Anthropic SDK |
| `openai-chat` | OpenAI-compatible Chat client |
| `openai-responses` | OpenAI Responses/Codex-compatible client |
| `gemini-generate-content` | Gemini-compatible client |
| `google-interactions` | Google Interactions-compatible client |

## Provider format / concrete profile mapping

| Provider format | Concrete vendor/profile | Endpoint |
| --- | --- | --- |
| `anthropic-messages` | Anthropic、MiniMax Anthropic-compatible | `/v1/messages` |
| `openai-chat` | OpenAI Chat、xAI Chat、OpenAI-compatible | `/v1/chat/completions` |
| `openai-responses` | OpenAI Responses、xAI Responses | `/v1/responses` |
| `gemini-generate-content` | Gemini、Vertex/AI Studio GenerateContent | `generateContent` / `streamGenerateContent` |
| `google-interactions` | Gemini Interactions | `/interactions` |
| `openai-codex-responses` | ChatGPT Codex OAuth backend | `/backend-api/codex/responses` / `/codex/responses` |
| `antigravity` | Antigravity | provider-native wrapper with `project`, `model`, `request` |
| `cursor-connect-rpc` | Cursor | HTTP/2 `application/connect+proto` `StreamUnifiedChatWithTools` |

## Concrete provider IDs

| Source | Provider/profile ID | Provider wire format |
| --- | --- | --- |
| `auth2api` | `anthropic` | `anthropic-messages` |
| `auth2api` | `codex` | `openai-codex-responses` |
| `auth2api` | `cursor` | `cursor-connect-rpc` |
| `agentSDK current` | `anthropic`、`minimax` | `anthropic-messages` |
| `agentSDK current` | `openai-api` | default `openai-responses`；qualified `openai-chat/*` 可強制 `openai-chat` |
| `agentSDK current` | `openai-codex-oauth` | `openai-codex-responses` normalization over Responses |
| `agentSDK current` | `xai` | default `openai-responses`；qualified `xai-chat/*` 可強制 `openai-chat` |
| `CLIProxyAPI` | `claude` | `anthropic-messages` |
| `CLIProxyAPI` | `codex` | `openai-codex-responses` |
| `CLIProxyAPI` | `gemini` / `gemini-interactions` | `gemini-generate-content` / `google-interactions` |
| `CLIProxyAPI` | `antigravity` | `antigravity` |
| `CLIProxyAPI` | OpenAI-compatible / `kimi` | `openai-chat`；部分 upstream route 使用 `openai-responses` |
| `CLIProxyAPI` | `xai` HTTP Responses path | `openai-responses` |

## 全部組合

| # | Entity | Sources |
| ---: | --- | --- |
| 1 | [`anthropic-messages__anthropic-messages`](anthropic-messages__anthropic-messages/) | `AC, AM, A2` |
| 2 | [`anthropic-messages__openai-chat`](anthropic-messages__openai-chat/) | `CP, AC, AM` |
| 3 | [`anthropic-messages__openai-responses`](anthropic-messages__openai-responses/) | `AC` |
| 4 | [`anthropic-messages__gemini-generate-content`](anthropic-messages__gemini-generate-content/) | `CP` |
| 5 | [`anthropic-messages__google-interactions`](anthropic-messages__google-interactions/) | `CP` |
| 6 | [`anthropic-messages__openai-codex-responses`](anthropic-messages__openai-codex-responses/) | `CP, AC, AM, A2` |
| 7 | [`anthropic-messages__antigravity`](anthropic-messages__antigravity/) | `CP` |
| 8 | [`anthropic-messages__cursor-connect-rpc`](anthropic-messages__cursor-connect-rpc/) | `A2` |
| 9 | [`openai-chat__anthropic-messages`](openai-chat__anthropic-messages/) | `CP, AC, AM, A2` |
| 10 | [`openai-chat__openai-chat`](openai-chat__openai-chat/) | `CP, AC, AM` |
| 11 | [`openai-chat__openai-responses`](openai-chat__openai-responses/) | `AC` |
| 12 | [`openai-chat__gemini-generate-content`](openai-chat__gemini-generate-content/) | `CP` |
| 13 | [`openai-chat__google-interactions`](openai-chat__google-interactions/) | `CP` |
| 14 | [`openai-chat__openai-codex-responses`](openai-chat__openai-codex-responses/) | `CP, AC, A2` |
| 15 | [`openai-chat__antigravity`](openai-chat__antigravity/) | `CP` |
| 16 | [`openai-chat__cursor-connect-rpc`](openai-chat__cursor-connect-rpc/) | `A2` |
| 17 | [`openai-responses__anthropic-messages`](openai-responses__anthropic-messages/) | `CP, AC, AM, A2` |
| 18 | [`openai-responses__openai-chat`](openai-responses__openai-chat/) | `CP, AC` |
| 19 | [`openai-responses__openai-responses`](openai-responses__openai-responses/) | `AC, AM` |
| 20 | [`openai-responses__gemini-generate-content`](openai-responses__gemini-generate-content/) | `CP` |
| 21 | [`openai-responses__google-interactions`](openai-responses__google-interactions/) | `CP` |
| 22 | [`openai-responses__openai-codex-responses`](openai-responses__openai-codex-responses/) | `CP, AC, AM, A2` |
| 23 | [`openai-responses__antigravity`](openai-responses__antigravity/) | `CP` |
| 24 | [`openai-responses__cursor-connect-rpc`](openai-responses__cursor-connect-rpc/) | `A2` |
| 25 | [`gemini-generate-content__anthropic-messages`](gemini-generate-content__anthropic-messages/) | `CP` |
| 26 | [`gemini-generate-content__openai-chat`](gemini-generate-content__openai-chat/) | `CP` |
| 27 | [`gemini-generate-content__gemini-generate-content`](gemini-generate-content__gemini-generate-content/) | `CP` |
| 28 | [`gemini-generate-content__google-interactions`](gemini-generate-content__google-interactions/) | `CP` |
| 29 | [`gemini-generate-content__openai-codex-responses`](gemini-generate-content__openai-codex-responses/) | `CP` |
| 30 | [`gemini-generate-content__antigravity`](gemini-generate-content__antigravity/) | `CP` |
| 31 | [`google-interactions__anthropic-messages`](google-interactions__anthropic-messages/) | `CP` |
| 32 | [`google-interactions__openai-chat`](google-interactions__openai-chat/) | `CP` |
| 33 | [`google-interactions__openai-responses`](google-interactions__openai-responses/) | `CP` |
| 34 | [`google-interactions__gemini-generate-content`](google-interactions__gemini-generate-content/) | `CP` |
| 35 | [`google-interactions__google-interactions`](google-interactions__google-interactions/) | `CP` |
| 36 | [`google-interactions__openai-codex-responses`](google-interactions__openai-codex-responses/) | `CP` |
| 37 | [`google-interactions__antigravity`](google-interactions__antigravity/) | `CP` |

## 每個 entity 的檔案

| File | Direction / payload type |
| --- | --- |
| `chat-message.md` | client text request → provider request；provider text response → client response |
| `chat-message-with-image.md` | client image input → provider image input；不支援時記錄實際降級/丟棄 |
| `tool-call.md` | provider tool call → client tool call → client tool result → provider tool result |
| `stream.md` | provider stream frames → client stream frames |
| `error.md` | provider error → client public error |
| `provider-normalization-variants.md` | 僅 Codex provider entities；逐來源列出 normalization 差異 |

## Scope

- 包含 chat/message inference 的主要 text、image input、function tool、stream 與 error shape。
- 不包含 auth token、models list、admin API、image-generation/video endpoint、WebSocket control frame 或完整第三方 schema。
- `Cursor` source 明確只支援文字；image/tool 會在 entity 檔案標示，不虛構 Connect-RPC 欄位。
- Identity pair 仍列為 entity，因為 source code 會 decode/normalize，不能視為無條件 raw passthrough。
