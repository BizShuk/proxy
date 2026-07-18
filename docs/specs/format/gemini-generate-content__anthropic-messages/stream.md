# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"provider-model","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}

event: message_stop
data: {"type":"message_stop"}
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
