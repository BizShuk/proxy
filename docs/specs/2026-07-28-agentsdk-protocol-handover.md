# 架構規格 — agentsdk-protocol-handover

狀態：`Landed`

日期：`2026-07-28`

## 1. 目標與範圍 (Goal & Scope)

目標：

- 讓 `agentsdk` 成為通用 SSE framing primitive 的單一 owner。
- 評估公開的 `provider/protocol/openaichat` 是否能取代
  `proxy/model/chat/types.go`。
- 將「上游 API 已落地」與「provider / proxy 已採用」分開驗收。

不在本計畫範圍：

- 不把 `proxy/svc/transform` 的 pairwise translation 上移。
- 不把 provider-specific terminal event 放進 generic SSE package。
- 不因 DTO 欄位相似就合併 Anthropic、Responses 或其他 vendor wire model。
- 不在同一輪要求 callers 直接改 import；compatibility facade 保留既有
  `model.SSEFrame` API。

## 2. 現況架構 (Current Architecture)

已確認的版本與程式碼：

| 邊界 | 現況 |
| --- | --- |
| `proxy` dependency | `go.mod` 使用 `github.com/bizshuk/agentsdk v0.0.24` |
| `proxy` SSE | `model/sse.go` 僅保留 `sse.Frame` / `Decoder` / errors / constants 的 compatibility facade |
| `agentsdk` SSE | `provider/protocol/sse` 單一擁有完整 frame、BOM、multiline data、大小限制與 writer |
| `agentsdk` parser adoption | `openaichat`、Anthropic、Antigravity、Codex、Grok、MiniMax 均採用共用 decoder |
| `agentsdk` OpenAI Chat | `v0.0.17` 已將 `provider/internal/openaichat` 移至公開的 `provider/protocol/openaichat` |
| `proxy` OpenAI Chat | `model/chat/types.go` 保存 client / upstream wire DTO，供 `3×3` pairwise transform 使用 |

```mermaid
flowchart LR
    U["Provider SSE bytes"] -->|"framing"| AS["agentsdk provider/protocol/sse"]
    AS -->|"Frame"| AC["agentsdk provider/protocol/openaichat"]
    AC -->|"ModelChunk"| AO["Google / Ollama adapters"]
    P["proxy upstream bytes"] -->|"compatibility wrapper"| PS["proxy model/sse facade"]
    PS -->|"SSEFrame"| PT["proxy pairwise transforms"]
    PT -->|"SSEFrame"| PC["proxy client"]
```

## 3. 架構位置與邊界 (Placement & Boundaries)

目標依賴方向：

```text
proxy
├── agentsdk/provider/protocol/sse
│   └── Go stdlib
└── proxy/model/chat
    └── proxy pairwise transform DTO

agentsdk provider adapters
├── provider/protocol/sse
└── provider-specific payload / terminal semantics
```

Owner 規則：

- `agentsdk/provider/protocol/sse`
  - 擁有空行 frame boundary、multiline `data`、UTF-8 BOM、`event`、`id`、
    `retry`、comments、line / frame 上限與 writer。
  - 不解讀 `[DONE]`、`message_stop`、`response.completed`。
- `agentsdk/provider/protocol/openaichat`
  - 擁有已由 Google / Ollama contract tests 證明相容的
    `core.Model* ↔ OpenAI Chat wire` projection。
  - 不成為所有 OpenAI-compatible wire DTO 的公共 schema。
- `proxy/model/chat`
  - 擁有 pairwise translation 所需的完整 Chat wire shape。
  - 不繞經 `core.Model*`，避免不可逆的 semantic loss。
- `proxy/svc/transform`
  - 保留來源格式與目標格式之間的 translation、stream state 與 terminal semantics。

## 4. 介面與資料流 (Interfaces & Data Flow)

公開 contract 控制在四組：

| Contract | 責任 |
| --- | --- |
| `sse.Frame` | 完整 transport frame，不含 provider payload semantics |
| `sse.NewDecoder` / `NewBoundedDecoder` + `Decoder.Next` | bounded frame decoding |
| `sse.Write` | frame encoding |
| `openaichat.EncodeRequest` / `DecodeResponse` / `ParseStream` | provider-neutral `core.Model*` projection |

```mermaid
flowchart LR
    N["Network bytes"] -->|"Next"| D["sse.Decoder"]
    D -->|"Frame"| V["Vendor protocol parser"]
    V -->|"terminal / JSON semantics"| C["ModelChunk or proxy transform"]
    C -->|"Frame"| W["sse.Write"]
```

## 5. 評估結論 (Assessment)

### 5.1 SSE primitive

結論：`採用`。

`agentsdk/provider/protocol/sse` 與 `proxy/model/sse.go` 的 transport contract
相同，且上游額外覆蓋 nil reader。`proxy` 不應再維護第二份實作。

遷移已於 `agentsdk v0.0.24` 與 proxy 完成：

- agentsdk 的 `openaichat`、Anthropic、Antigravity、Codex、Grok、MiniMax parser
  均使用 `sse.Decoder`。
- proxy 的 `model/sse.go` 只保留型別 / 錯誤 alias 與 constructor / writer wrapper，
  原有 callers 不需改動。
- `[DONE]`、`message_stop`、`response.completed` 等 terminal semantics 仍由各
  provider / format consumer 判讀。

### 5.2 公開 `openaichat`

結論：`公開已完成，但不取代 proxy/model/chat`。

兩者的責任不同：

| 能力 | `agentsdk/provider/protocol/openaichat` | `proxy/model/chat` |
| --- | --- | --- |
| 輸入 / 輸出 | `core.ModelRequest` / `ModelResult` / `ModelChunk` | 完整 Chat wire DTO |
| DTO visibility | request / response / stream DTO 為 private | translation 可直接操作 exported DTO |
| message content | canonical text / tools projection | string-or-multipart union，含 image URL |
| request controls | adapter 使用的子集合 | `max_completion_tokens`、`top_p`、`stop`、`tool_choice`、`reasoning_effort`、`parallel_tool_calls` 等 |
| response fidelity | fold 成 runtime result | 保留 `id`、`object`、`created`、`model`、choices、usage details |
| stream behavior | fold 成 channel；framing error 以關閉 channel 表達 | pairwise transform 需保留 choice index、finish reason、usage，並把錯誤編成來源格式 |

因此 `proxy` 若直接重用 `openaichat`，會先把 wire payload 投影成
`core.Model*` 再重建另一種 wire format；這會丟失 translation 所需資訊。
除非未來至少兩個 lossless wire consumer 以 golden tests 證明完全相容，否則不再要求
agentsdk 擴大公開 DTO surface。

## 6. 漸進落地步驟 (Incremental Steps)

1. `已完成`：agentsdk 以 `v0.0.17` 公開 `provider/protocol/openaichat`，以
   `v0.0.19` 公開 `provider/protocol/sse`。
2. `已完成`：agentsdk `v0.0.24` 的五個剩餘 parser 已加入共用 decoder，
   provider-specific terminal semantics 維持在 consumer。
3. `已完成`：proxy 升至 `v0.0.24`，`model/sse.go` 改為 `sse.Frame` / `Decoder` /
   errors / constants 的 compatibility aliases 與薄 wrapper。
4. `已完成`：`model`、`svc/transform`、`handlers` 與完整 repo verification 通過；
   callers 目前保留 compatibility facade，避免不必要的跨 package churn。
5. `維持原決策`：`proxy/model/chat/types.go` 留在原位，不採用有損的
   `openaichat` projection。

## 7. 驗收與回滾 (Verification & Rollback)

完成證據：

- `go mod download -json github.com/bizshuk/agentsdk@v0.0.24`：確認 tag
  `v0.0.24` 與 module checksum。
- `go test ./model ./svc/transform ./handlers -count=1 -timeout=180s`：通過。
- `npm run verify`：完整 test、build、vet 通過。

固定驗證 gate：

```bash
go test ./model ./svc/transform ./handlers
go test ./...
git diff --check
```

若任一 provider 的 observable stream contract 改變，回滾該 provider 的 consumer
改動即可；共用 `sse` package 與其他已遷移 provider 不需一起回滾。
