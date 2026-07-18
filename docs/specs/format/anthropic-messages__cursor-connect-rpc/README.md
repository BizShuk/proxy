# Anthropic Messages → Cursor Connect-RPC

`entity`：`anthropic-messages__cursor-connect-rpc`

| Boundary | Format |
| --- | --- |
| client send / receive | `anthropic-messages` |
| provider receive / send | `cursor-connect-rpc` |
| client endpoint | `POST /v1/messages` |
| provider endpoint | `POST /aiserver.v1.ChatService/StreamUnifiedChatWithTools` |

## Payload files

- [文字訊息](chat-message.md)
- [圖片輸入](chat-message-with-image.md)
- [工具呼叫循環](tool-call.md)
- [串流](stream.md)
- [錯誤](error.md)

## Source evidence

- `auth2api main@37c9b864`：[src/upstream/cursor-api.ts](../../../../../../tmp/auth2api/src/upstream/cursor-api.ts) — client payload 轉 Cursor Connect-RPC；回應重編碼為 client SSE

## Interpretation

- `client send` 是 proxy 接收到的 payload。
- `provider receive` 是 transform/normalizer 後送往 upstream 的 payload。
- `provider send` 是 upstream 回傳的 payload/frame。
- `client receive` 是 reverse transform 後回給 caller 的 payload/frame。

