# 文字訊息 (Chat Message)

`方向`：client send → provider receive → provider send → client receive。

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
  "model": "provider-model",
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

## 3. Provider send

```json
{
  "id": "interaction_1",
  "object": "interaction",
  "model": "provider-model",
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

## Notes

- request model 由 `client-model` route/normalize 成 `provider-model`。
- 欄位只列四個來源實作有讀寫的代表性 subset。
