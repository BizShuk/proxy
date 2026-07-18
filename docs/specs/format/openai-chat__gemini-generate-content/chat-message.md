# 文字訊息 (Chat Message)

`方向`：client send → provider receive → provider send → client receive。

## 1. Client send

```json
{
  "model": "client-model",
  "messages": [
    {
      "role": "user",
      "content": "Inspect a.txt"
    }
  ],
  "stream": false
}
```

## 2. Provider receive

```json
{
  "model": "provider-model",
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "Inspect a.txt"
        }
      ]
    }
  ]
}
```

## 3. Provider send

```json
{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [
          {
            "text": "done"
          }
        ]
      },
      "finishReason": "STOP"
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 5,
    "candidatesTokenCount": 1,
    "totalTokenCount": 6
  },
  "modelVersion": "provider-model"
}
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

## Notes

- request model 由 `client-model` route/normalize 成 `provider-model`。
- 欄位只列四個來源實作有讀寫的代表性 subset。
