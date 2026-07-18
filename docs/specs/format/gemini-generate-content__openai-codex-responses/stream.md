# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"done"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"provider-model","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}

data: [DONE]
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
