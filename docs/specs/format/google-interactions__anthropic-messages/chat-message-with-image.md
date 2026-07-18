# 圖片輸入 (Chat Message with Image)

`方向`：client image request → provider request → provider text response → client text response。

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
          "text": "Describe this image"
        },
        {
          "type": "image",
          "mime_type": "image/png",
          "data": "aW1n"
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

## 3. Provider send

```json
{
  "id": "msg_1",
  "type": "message",
  "role": "assistant",
  "model": "provider-model",
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

## Support

- base64 data URL / inline data 依 pair translator 轉成 provider native image part。
- 本檔是 image input；不宣稱 provider 會產生 image output。

