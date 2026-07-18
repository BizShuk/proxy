# 工具呼叫循環 (Tool Call Cycle)

`方向`：provider tool call → client tool call → client tool result → provider tool result。

## 1. Provider send: tool call

```json
{
  "response": {
    "candidates": [
      {
        "content": {
          "role": "model",
          "parts": [
            {
              "functionCall": {
                "name": "read",
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
}
```

## 2. Client receive: tool call

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
        "content": null,
        "tool_calls": [
          {
            "id": "call_1",
            "type": "function",
            "function": {
              "name": "read",
              "arguments": "{\"path\":\"a.txt\"}"
            }
          }
        ]
      },
      "finish_reason": "tool_calls"
    }
  ],
  "usage": {
    "prompt_tokens": 5,
    "completion_tokens": 4,
    "total_tokens": 9
  }
}
```

## 3. Client send: tool result

```json
{
  "model": "client-model",
  "messages": [
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "call_1",
          "type": "function",
          "function": {
            "name": "read",
            "arguments": "{\"path\":\"a.txt\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_1",
      "name": "read",
      "content": "ok"
    }
  ],
  "tools": [
    {
      "type": "function",
      "function": {
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
    }
  ]
}
```

## 4. Provider receive: tool result

```json
{
  "project": "",
  "model": "provider-model",
  "request": {
    "contents": [
      {
        "role": "model",
        "parts": [
          {
            "functionCall": {
              "name": "read",
              "args": {
                "path": "a.txt"
              }
            },
            "thoughtSignature": "skip_thought_signature_validator"
          }
        ]
      },
      {
        "role": "user",
        "parts": [
          {
            "functionResponse": {
              "name": "read",
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
    ]
  }
}
```

## Support

- function name、call ID 與 JSON arguments 在四個 boundary 間保留。
- `arguments` 在 OpenAI formats 是 JSON string；Anthropic/Gemini/Interactions 使用 JSON object。

