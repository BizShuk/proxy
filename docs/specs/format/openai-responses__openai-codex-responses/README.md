# OpenAI Responses → OpenAI Codex Responses

`entity`：`openai-responses__openai-codex-responses`

| Boundary | Format |
| --- | --- |
| client send / receive | `openai-responses` |
| provider receive / send | `openai-codex-responses` |
| client endpoint | `POST /v1/responses` |
| provider endpoint | `POST /backend-api/codex/responses 或 /codex/responses` |

## Payload files

- [文字訊息](chat-message.md)
- [圖片輸入](chat-message-with-image.md)
- [工具呼叫循環](tool-call.md)
- [串流](stream.md)
- [錯誤](error.md)
- [Provider normalization variants](provider-normalization-variants.md)

## Source evidence

- `CLIProxyAPI main@411d7d41`：[internal/translator/codex/openai/responses/init.go](../../../../../../tmp/CLIProxyAPI/internal/translator/codex/openai/responses/init.go) — 顯式 translator registry pair
- `agentSDK current@39a913cb`：[proxy/svc/upstream/profile.go](../../../../svc/upstream/profile.go) — OpenAI Responses transform 後套用 Codex normalizer
- `agentSDK master@e7edfc7c`：`git show e7edfc7c:proxy/adaptor/adaptor.go` — master monolithic adaptor route/translator
- `auth2api main@37c9b864`：[src/upstream/responses-translator.ts](../../../../../../tmp/auth2api/src/upstream/responses-translator.ts) — client handler 轉 Codex Responses upstream

## Interpretation

- `client send` 是 proxy 接收到的 payload。
- `provider receive` 是 transform/normalizer 後送往 upstream 的 payload。
- `provider send` 是 upstream 回傳的 payload/frame。
- `client receive` 是 reverse transform 後回給 caller 的 payload/frame。
