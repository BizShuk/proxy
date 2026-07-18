# 圖片輸入 (Chat Message with Image)

`方向`：client image request → provider request → provider text response → client text response。

## 1. Client send

```json
{
  "model": "client-model",
  "max_tokens": 512,
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "Describe this image"
        },
        {
          "type": "image",
          "source": {
            "type": "base64",
            "media_type": "image/png",
            "data": "aW1n"
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
  "id": "msg_1",
  "type": "message",
  "role": "assistant",
  "model": "client-model",
  "content": [
    {
      "type": "text",
      "text": "done"
    }
  ],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 5,
    "output_tokens": 1
  }
}
```

## Support

- `unsupported`：`auth2api` 的 Cursor `messagesFromBody` 只收集 `text/input_text`；image bytes 不進 Connect-RPC protobuf。
- 本檔是 image input；不宣稱 provider 會產生 image output。

