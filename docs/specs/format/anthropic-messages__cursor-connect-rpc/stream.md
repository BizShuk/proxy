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
event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"client-model","stop_reason":null,"usage":{"input_tokens":5,"output_tokens":0}}}

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

## Framing rules

- Provider side 是 Connect-RPC 5-byte frame header + protobuf payload，不是 SSE。
- Client terminal event 是 `message_stop`。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
