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
- 欄位只列四個來源實作有讀寫的代表性 subset。
