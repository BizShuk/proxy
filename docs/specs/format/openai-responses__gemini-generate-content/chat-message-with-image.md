# 圖片輸入 (Chat Message with Image)

`方向`：client image request → provider request → provider text response → client text response。

## 1. Client send

```json
{
  "model": "client-model",
  "input": [
    {
      "type": "message",
      "role": "user",
      "content": [
        {
          "type": "input_text",
          "text": "Describe this image"
        },
        {
          "type": "input_image",
          "image_url": "data:image/png;base64,aW1n"
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
          "text": "Describe this image"
        },
        {
          "inlineData": {
            "mimeType": "image/png",
            "data": "aW1n"
          }
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
  "id": "resp_1",
  "object": "response",
  "model": "client-model",
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

## Support

- base64 data URL / inline data 依 pair translator 轉成 provider native image part。
- 本檔是 image input；不宣稱 provider 會產生 image output。

