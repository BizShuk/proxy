# OpenAI Chat Completions → Google Interactions

`entity`：`openai-chat__google-interactions`

| Boundary | Format |
| --- | --- |
| client send / receive | `openai-chat` |
| provider receive / send | `google-interactions` |
| client endpoint | `POST /v1/chat/completions` |
| provider endpoint | `POST /interactions` |

## Payload files

- [文字訊息](chat-message.md)
- [圖片輸入](chat-message-with-image.md)
- [工具呼叫循環](tool-call.md)
- [串流](stream.md)
- [錯誤](error.md)

## Source evidence

- `CLIProxyAPI main@411d7d41`：[internal/translator/openai/interactions/chat-completions/init.go](../../../../../../tmp/CLIProxyAPI/internal/translator/openai/interactions/chat-completions/init.go) — 顯式 translator registry pair

## Interpretation

- `client send` 是 proxy 接收到的 payload。
- `provider receive` 是 transform/normalizer 後送往 upstream 的 payload。
- `provider send` 是 upstream 回傳的 payload/frame。
- `client receive` 是 reverse transform 後回給 caller 的 payload/frame。

