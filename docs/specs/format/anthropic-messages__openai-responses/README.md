# Anthropic Messages → OpenAI Responses

`entity`：`anthropic-messages__openai-responses`

| Boundary | Format |
| --- | --- |
| client send / receive | `anthropic-messages` |
| provider receive / send | `openai-responses` |
| client endpoint | `POST /v1/messages` |
| provider endpoint | `POST /v1/responses` |

## Payload files

- [文字訊息](chat-message.md)
- [圖片輸入](chat-message-with-image.md)
- [工具呼叫循環](tool-call.md)
- [串流](stream.md)
- [錯誤](error.md)

## Source evidence

- `agentSDK current@39a913cb`：[proxy/svc/transform/default.go](../../../../svc/transform/default.go) — 完整 3×3 pairwise registry

## Interpretation

- `client send` 是 proxy 接收到的 payload。
- `provider receive` 是 transform/normalizer 後送往 upstream 的 payload。
- `provider send` 是 upstream 回傳的 payload/frame。
- `client receive` 是 reverse transform 後回給 caller 的 payload/frame。

