# 文字訊息 (Chat Message)

`方向`：client send → provider receive → provider send → client receive。

## 1. Client send

```json
{
  "model": "client-model",
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

## 2. Provider receive

```json
{
  "model": "provider-model",
  "input": [
    {
      "type": "message",
      "role": "user",
      "content": [
        {
          "type": "input_text",
          "text": "Inspect a.txt"
        }
      ]
    }
  ],
  "instructions": "",
  "stream": true,
  "store": false,
  "parallel_tool_calls": true,
  "include": [
    "reasoning.encrypted_content"
  ]
}
```

## 3. Provider send

```json
{
  "id": "resp_1",
  "object": "response",
  "model": "provider-model",
  "status": "completed",
  "output": [
    {
      "id": "msg_1",
      "type": "message",
      "role": "assistant",
      "status": "completed",
      "content": [
        {
          "type": "output_text",
          "text": "done"
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 5,
    "output_tokens": 1,
    "total_tokens": 6
  }
}
```

## 4. Client receive

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
  "modelVersion": "client-model"
}
```

## Notes

- request model 由 `client-model` route/normalize 成 `provider-model`。
- Codex upstream 強制 `stream: true`、`store: false`；non-stream client 由 proxy 收斂 SSE。
- 本檔 provider payload 採 `CLIProxyAPI` variant；其他來源差異見 [provider normalization variants](provider-normalization-variants.md)。
- 欄位只列四個來源實作有讀寫的代表性 subset。
