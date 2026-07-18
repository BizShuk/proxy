# 串流回應 (Streaming Response)

`方向`：provider stream frame → pairwise stateful transform → client stream frame。

## 1. Provider send

```text
Connect data frame 1:
  flags: 0x00
  protobuf field 2 -> field 1: "do"

Connect data frame 2:
  flags: 0x00
  protobuf field 2 -> field 1: "ne"

Connect end frame:
  flags: 0x02
  JSON metadata: {}
```

## 2. Client receive

```text
event: response.created
data: {"type":"response.created","response":{"id":"resp_1","model":"client-model","status":"in_progress"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"done"}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_1","object":"response","model":"client-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],"usage":{"input_tokens":5,"output_tokens":1,"total_tokens":6}}}

data: [DONE]
```

## Framing rules

- Provider side 是 Connect-RPC 5-byte frame header + protobuf payload，不是 SSE。
- Client terminal event 是 `response.completed`，其後可有 `[DONE]`。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
