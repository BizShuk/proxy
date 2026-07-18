# 工具呼叫循環 (Tool Call Cycle)

`方向`：provider tool call → client tool call → client tool result → provider tool result。

## 1. Provider send: tool call

```json
{
  "candidates": [
    {
      "content": {
        "role": "model",
        "parts": [
          {
            "functionCall": {
              "name": "read",
              "id": "call_1",
              "args": {
                "path": "a.txt"
              }
            }
          }
        ]
      },
      "finishReason": "STOP"
    }
  ],
  "usageMetadata": {
    "promptTokenCount": 5,
    "candidatesTokenCount": 4,
    "totalTokenCount": 9
  },
  "modelVersion": "provider-model"
}
```

## 2. Client receive: tool call

```json
{
  "id": "resp_1",
  "object": "response",
  "model": "client-model",
  "status": "completed",
  "output": [
    {
      "id": "fc_1",
      "type": "function_call",
      "call_id": "call_1",
      "name": "read",
      "arguments": "{\"path\":\"a.txt\"}"
    }
  ],
  "usage": {
    "input_tokens": 5,
    "output_tokens": 4,
    "total_tokens": 9
  }
}
```

## 3. Client send: tool result

```json
{
  "model": "client-model",
  "input": [
    {
      "type": "function_call",
      "call_id": "call_1",
      "name": "read",
      "arguments": "{\"path\":\"a.txt\"}"
    },
    {
      "type": "function_call_output",
      "call_id": "call_1",
      "output": "ok"
    }
  ],
  "tools": [
    {
      "type": "function",
      "name": "read",
      "description": "Read a file",
      "parameters": {
        "type": "object",
        "properties": {
          "path": {
            "type": "string"
          }
        },
        "required": [
          "path"
        ]
      }
    }
  ]
}
```

## 4. Provider receive: tool result

```json
{
  "model": "provider-model",
  "contents": [
    {
      "role": "model",
      "parts": [
        {
          "functionCall": {
            "name": "read",
            "id": "call_1",
            "args": {
              "path": "a.txt"
            }
          }
        }
      ]
    },
    {
      "role": "function",
      "parts": [
        {
          "functionResponse": {
            "name": "read",
            "id": "call_1",
            "response": {
              "result": "ok"
            }
          }
        }
      ]
    }
  ],
  "tools": [
    {
      "functionDeclarations": [
        {
          "name": "read",
          "description": "Read a file",
          "parametersJsonSchema": {
            "type": "object",
            "properties": {
              "path": {
                "type": "string"
              }
            },
            "required": [
              "path"
            ]
          }
        }
      ]
    }
  ]
}
```

## Support

- function name、call ID 與 JSON arguments 在四個 boundary 間保留。
- `arguments` 在 OpenAI formats 是 JSON string；Anthropic/Gemini/Interactions 使用 JSON object。

