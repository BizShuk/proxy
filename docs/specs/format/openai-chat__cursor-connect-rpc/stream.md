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
data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}

data: {"id":"chat_1","object":"chat.completion.chunk","created":1,"model":"client-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

## Framing rules

- Provider side 是 Connect-RPC 5-byte frame header + protobuf payload，不是 SSE。
- Client terminal sentinel 是 `data: [DONE]`。
- ID、tool argument fragments、usage 與 terminal state 需要由每個 request 的 stateful transformer 累積。
