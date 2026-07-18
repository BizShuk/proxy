# Provider Normalization Variants

`結論`：`openai-codex-responses` 的 base wire format 是 OpenAI Responses，但四個來源的 upstream normalization 不相同；不得把單一 JSON 當成所有實作的 byte-for-byte output。

| Source | Request normalization |
| --- | --- |
| `CLIProxyAPI main@411d7d41` | 強制 `stream: true`、`store: false`、`parallel_tool_calls: true`、`include: ["reasoning.encrypted_content"]`；刪除 token limit、`temperature`、`top_p`、非 priority `service_tier`、`truncation`、`user`；`system` role 改成 `developer`。 |
| `auth2api main@37c9b864` | 補 `stream: true`、`store: false`、`instructions: ""`；handler 再刪除 `max_output_tokens`、`parallel_tool_calls`、`user`。只涵蓋 Anthropic/OpenAI Chat/OpenAI Responses 三種 client。 |
| `agentSDK current@39a913cb` | 強制 `stream: true`、`store: false`；把 `system/developer` message 提升到 `instructions`，non-stream client 設 `BridgeToNonStream`。不額外強制 `include` 或 `parallel_tool_calls`。只涵蓋核心 3×3 client。 |
| `agentSDK master@e7edfc7c` | legacy handler 沒有統一 normalizer；Anthropic → Responses 設 `store: false`，但 `stream` 沿用 client 值。此差異是 master 的既有行為，不是 current contract。 |

## Representative payloads

`CLIProxyAPI` variant（本 entity 的其他範例採此 shape）：

```json
{
  "model": "provider-model",
  "input": [
    {
      "type": "message",
      "role": "user",
      "content": [
        {"type": "input_text", "text": "Inspect a.txt"}
      ]
    }
  ],
  "instructions": "",
  "stream": true,
  "store": false,
  "parallel_tool_calls": true,
  "include": ["reasoning.encrypted_content"]
}
```

`auth2api` / `agentSDK current` common minimum：

```json
{
  "model": "provider-model",
  "input": [
    {
      "type": "message",
      "role": "user",
      "content": [
        {"type": "input_text", "text": "Inspect a.txt"}
      ]
    }
  ],
  "instructions": "",
  "stream": true,
  "store": false
}
```

## Evidence

- [CLIProxyAPI Codex converter](../../../../../../tmp/CLIProxyAPI/internal/translator/codex/openai/responses/codex_openai-responses_request.go)
- [auth2api Codex normalizer](../../../../../../tmp/auth2api/src/upstream/codex-api.ts)
- [auth2api handlers](../../../../../../tmp/auth2api/src/handlers/openai.ts)
- [agentSDK current profile](../../../../svc/upstream/profile.go)
- `agentSDK master`：`git show e7edfc7c:proxy/adaptor/translator.go`
