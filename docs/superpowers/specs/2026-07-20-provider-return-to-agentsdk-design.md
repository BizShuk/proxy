# Provider 回歸 agentSDK 設計

## Context

`proxy/providers` 曾被抽成獨立本地模組 `github.com/bizshuk/llm_provider`，並複製一份 `agentSDK/core`。目前決定撤銷此邊界：全部 provider 回歸 `github.com/bizshuk/agentsdk/provider/*`，proxy 直接依賴 agentSDK。

`llm_provider` 由原 proxy provider 複製而來，包含較新的 codex、google、minimax 等實作；它是本次回寫的唯一真相來源。agentSDK 現有 provider 不作逐檔 merge，避免殘留舊版檔案或形成混合實作。

## Target Architecture

```text
agentSDK/core
    ↑
agentSDK/provider/{anthropic,antigravity,codex,google,grok,minimax,ollama}
    ↑
proxy/svc/upstream dispatcher
```

- `agentSDK/provider/*` 是唯一 provider 實作。
- provider import `github.com/bizshuk/agentsdk/core`。
- proxy import `github.com/bizshuk/agentsdk/core` 與 `github.com/bizshuk/agentsdk/provider/<name>`。
- `github.com/bizshuk/llm_provider` 模組與目錄在驗證後移除。
- proxy 不恢復自己的 `providers/` 目錄。

## Provider Migration

1. 以 `~/projects/ai/llm_provider` 的七個 provider 目錄建立 staged copy。
2. 在 staged copy 內改寫 import：
   - `github.com/bizshuk/llm_provider/core` → `github.com/bizshuk/agentsdk/core`
   - `github.com/bizshuk/llm_provider/<name>` → `github.com/bizshuk/agentsdk/provider/<name>`
3. staged copy 通過格式與 import 殘留檢查後，完整替換 `~/projects/agentSDK/provider`。
4. 不複製 `llm_provider/core`；沿用既有 `agentSDK/core`。
5. 不保留只存在於舊 agentSDK provider 的檔案，例如 google `json_helpers.go` 與 `translate_test.go`；完整替換確保實作與測試同源。

## Proxy Reconnection

proxy 的以下檔案改回 agentSDK import：

- `svc/upstream/dispatcher.go`
- `svc/upstream/dispatcher_default.go`
- `svc/upstream/dispatcher_oauth.go`
- `svc/upstream/dispatcher_test.go`
- `handlers/handler_dispatcher_test.go`

`proxy/go.mod` 恢復直接依賴 agentSDK，並以本地 replace 連結：

```go
require github.com/bizshuk/agentsdk <current-pseudo-version>
replace github.com/bizshuk/agentsdk => ../../agentSDK
```

`proxy/go.work` 使用 `.` 與 `../../agentSDK`；`agentSDK/go.work` 移除 `../ai/llm_provider`，保留其餘既有 use entries，不覆寫同時進行中的其他工作。

## Port Drift Test Fix

`proxy/cmd/cmd_test.go` 目前用 source-introspection 斷言 `cmd.go` 必須含 `opts.port`，但實作已簡化為局部變數 `port`：

```go
command.PersistentFlags().IntVar(&port, "port", DEFAULT_PORT, "Server port")
cfg.Server.Port = port
```

本次只修測試期望，使其對齊現有行為；不為了滿足字串測試而重引入 `opts` 結構。更新兩項斷言：

- `&opts.port` → `&port`
- `cfg.Server.Port=opts.port` → `cfg.Server.Port=port`

## Safety and Scope

- 不動 agentSDK 其他未提交變更，包括 `CLAUDE.md`、`README.md`、`cmd/root.go`、`main.go`、`tools/`。
- 不動 proxy 的 `tmp/CLIProxyAPI` 與其他非 provider 變更。
- 先複製與驗證，再刪除 `llm_provider`，避免真相來源提前消失。
- 不自動 commit；跨 repo 提交範圍由使用者另行決定。

## Verification

1. 確認 agentSDK/provider 已無 `llm_provider` import。
2. 執行 `gofmt`、`go build ./...` 與 `go test ./provider/...`（agentSDK）。
3. 執行 `go build ./...`、`go vet ./...`、`go test ./...`（proxy）。
4. 確認 proxy 與 agentSDK 全部 Go 原始碼均無 `github.com/bizshuk/llm_provider`。
5. 抽驗新版內容：
   - codex 包含 `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`。
   - google 包含 `auth_api.go`、`dto.go`、`stream.go`、`validate.go`。
6. 全部驗證完成後刪除 `~/projects/ai/llm_provider`，再重跑 proxy 與 agentSDK build，確保沒有隱性依賴。
