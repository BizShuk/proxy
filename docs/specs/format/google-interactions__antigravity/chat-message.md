# 文字訊息 (Chat Message)

`方向`：client send → provider receive → provider send → client receive。

## 1. Client send

```json
{
  "model": "client-model",
  "input": [
    {
      "type": "user_input",
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
  "id": "interaction_1",
  "object": "interaction",
  "model": "client-model",
  "status": "completed",
  "steps": [
    {
      "type": "model_output",
      "content": [
        {
          "type": "text",
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

## Notes

- request model 由 `client-model` route/normalize 成 `provider-model`。
- Antigravity upstream 在 Gemini request 外包一層 `project/model/request` envelope。
- 欄位只列四個來源實作有讀寫的代表性 subset。
