# 工具呼叫循環 (Tool Call Cycle)

`方向`：provider tool call → client tool call → client tool result → provider tool result。

## 1. Provider send: tool call

```json
{
  "id": "interaction_1",
  "object": "interaction",
  "model": "provider-model",
  "status": "completed",
  "steps": [
    {
      "type": "function_call",
      "id": "call_1",
      "call_id": "call_1",
      "name": "read",
      "arguments": {
        "path": "a.txt"
      }
    }
  ],
  "usage": {
    "input_tokens": 5,
    "output_tokens": 4,
    "total_tokens": 9
  }
}
```

## 2. Client receive: tool call

```json
{
  "id": "msg_1",
  "type": "message",
  "role": "assistant",
  "model": "client-model",
  "content": [
    {
      "type": "tool_use",
      "id": "call_1",
      "name": "read",
      "input": {
        "path": "a.txt"
      }
    }
  ],
  "stop_reason": "tool_use",
  "stop_sequence": null,
  "usage": {
    "input_tokens": 5,
    "output_tokens": 4
  }
}
```

## 3. Client send: tool result

```json
{
  "model": "client-model",
  "max_tokens": 512,
  "messages": [
    {
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "call_1",
          "name": "read",
          "input": {
            "path": "a.txt"
          }
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "call_1",
          "content": "ok"
        }
      ]
    }
  ],
  "tools": [
    {
      "name": "read",
      "description": "Read a file",
      "input_schema": {
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
  "input": [
    {
      "type": "function_call",
      "name": "read",
      "call_id": "call_1",
      "arguments": {
        "path": "a.txt"
      }
    },
    {
      "type": "function_result",
      "name": "read",
      "call_id": "call_1",
      "result": {
        "output": "ok"
      }
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

## Support

- function name、call ID 與 JSON arguments 在四個 boundary 間保留。
- `arguments` 在 OpenAI formats 是 JSON string；Anthropic/Gemini/Interactions 使用 JSON object。

