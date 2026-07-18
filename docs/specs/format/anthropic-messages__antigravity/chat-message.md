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

```json
{
  "project": "",
  "model": "provider-model",
  "request": {
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
}
```

## 3. Provider send

```json
{
  "response": {
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
}
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
- Antigravity upstream 在 Gemini request 外包一層 `project/model/request` envelope。
- 欄位只列四個來源實作有讀寫的代表性 subset。
