# 文字訊息 (Chat Message)

`方向`：client send → provider receive → provider send → client receive。

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
          "text": "Inspect a.txt"
        }
      ]
    }
  ],
  "stream": false
}
```

## 2. Provider receive

```text
HTTP/2 POST /aiserver.v1.ChatService/StreamUnifiedChatWithTools
Content-Type: application/connect+proto
Connect-Protocol-Version: 1

Connect frame:
  flags: 0x00
  length: <big-endian uint32>
  protobuf field 1 (request wrapper):
    field 1 (repeated message):
      field 1: "Inspect a.txt"
      field 2: 1
      field 13: "<message UUID>"
      field 47: 2
    field 5 (model):
      field 1: "provider-model"
    field 23: "<conversation UUID>"
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

## Notes

- request model 由 `client-model` route/normalize 成 `provider-model`。
- Cursor upstream 是 HTTP/2 Connect-RPC binary；client JSON 不會原樣送出。
- 欄位只列四個來源實作有讀寫的代表性 subset。
