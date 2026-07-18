# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"modelVersion":"provider-model"}}

data: {"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"provider-model"}}
```

## 2. Client receive

```text
data: {"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"modelVersion":"client-model"}

data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"client-model"}
```

## Framing rules

- Provider side 以完整 frame/chunk 為單位解析，不把 multiline SSE data 當成獨立事件。
- Client 取得 Gemini candidate chunks，最後一個 chunk 帶 `finishReason`。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
