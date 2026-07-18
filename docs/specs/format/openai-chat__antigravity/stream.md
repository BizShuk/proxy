# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"modelVersion":"provider-model"}}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"provider-model"}}
```

## 2. Client receive

```text
data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}

data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

## Framing rules

- Provider side 以完整 frame/chunk 為單位解析，不把 multiline SSE data 當成獨立事件。
- Client terminal sentinel 是 `data: [DONE]`。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
