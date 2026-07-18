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

```json
{
  "model": "provider-model",
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

## 3. Provider send

```json
{
  "id": "chat_1",
  "object": "chat.completion",
  "created": 1,
  "model": "provider-model",
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

- base64 data URL / inline data 依 pair translator 轉成 provider native image part。
- 本檔是 image input；不宣稱 provider 會產生 image output。

