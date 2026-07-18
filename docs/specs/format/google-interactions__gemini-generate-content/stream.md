# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
data: {"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"modelVersion":"provider-model"}

data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1,"totalTokenCount":6},"modelVersion":"provider-model"}
```

## 2. Client receive

```text
event: step.start
data: {"index":0,"step":{"type":"model_output"},"event_type":"step.start"}

event: step.delta
data: {"index":0,"delta":{"type":"text","text":"done"},"event_type":"step.delta"}

event: step.stop
data: {"index":0,"event_type":"step.stop"}

event: interaction.completed
data: {"interaction":{"id":"interaction_1","object":"interaction","model":"client-model","status":"completed"},"event_type":"interaction.completed"}

event: done
data: [DONE]
```

## Framing rules

- Provider side 以完整 frame/chunk 為單位解析，不把 multiline SSE data 當成獨立事件。
- Client 取得 `step.*` 與 `interaction.completed` events。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
