# 圖片輸入 (Chat Message with Image)

`方向`：client image request → provider request → provider text response → client text response。

## 1. Client send

```json
{
  "model": "client-model",
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Describe this image"
        },
        {
          "type": "image_url",
          "image_url": {
            "url": "data:image/png;base64,aW1n"
          }
        }
      ]
    }
  ]
}
```

## 2. Provider receive

```text
HTTP/2 POST /aiserver.v1.ChatService/StreamUnifiedChatWithTools
Content-Type: application/connect+proto

Connect frame protobuf:
  message.content: "Describe this image"
  model: "provider-model"
  image bytes: <not present>
```

## 3. Provider send

```text
Connect frame:
  flags: 0x00
  length: <big-endian uint32>
  protobuf field 2:
    field 1: "done"

Connect end frame:
  flags: 0x02
  JSON metadata: {}
```

## 4. Client receive

```json
{
  "id": "chat_1",
  "object": "chat.completion",
  "created": 1,
  "model": "client-model",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "done"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 5,
    "completion_tokens": 1,
    "total_tokens": 6
  }
}
```

## Support

- `unsupported`：`auth2api` 的 Cursor `messagesFromBody` 只收集 `text/input_text`；image bytes 不進 Connect-RPC protobuf。
- 本檔是 image input；不宣稱 provider 會產生 image output。

