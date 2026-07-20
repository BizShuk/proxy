# Provider Return to agentSDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 將新版 provider 完整回寫至 agentSDK，讓 proxy 恢復直接引用 agentSDK，移除臨時 llm_provider 模組，並修正 port source-introspection 測試漂移。

**Architecture:** `agentSDK/core` 與 `agentSDK/provider/*` 恢復為唯一 provider 邊界；proxy 只 import `github.com/bizshuk/agentsdk/core` 與 `github.com/bizshuk/agentsdk/provider/<name>`。以 llm_provider 七個 provider 為 staged source，完成驗證後才刪除 llm_provider。

**Tech Stack:** Go 1.26.0、Go workspaces、Go modules、testify。

## Global Constraints

- 以 `~/projects/ai/llm_provider` 七個 provider 為唯一真相來源。
- 不複製 `llm_provider/core`；provider 使用 `agentSDK/core`。
- 完整替換 `agentSDK/provider`，不保留舊版專屬檔案。
- 不動 agentSDK 其他未提交變更：`CLAUDE.md`、`README.md`、`cmd/root.go`、`main.go`、`tools/` 等。
- 不動 proxy `tmp/CLIProxyAPI` 與其他非 provider 變更。
- 不 commit。

---

### Task 1: 完整替換 agentSDK provider

**Files:**
- Replace: `~/projects/agentSDK/provider/{anthropic,antigravity,codex,google,grok,minimax,ollama}/*`
- Source: `~/projects/ai/llm_provider/{anthropic,antigravity,codex,google,grok,minimax,ollama}/*`

**Interfaces:**
- Consumes: `github.com/bizshuk/agentsdk/core` 型別。
- Produces: `github.com/bizshuk/agentsdk/provider/<name>` 七個 provider package。

- [ ] **Step 1: 複製七個 provider 至 staged directory**

```bash
stage=$(mktemp -d)
for p in anthropic antigravity codex google grok minimax ollama; do
  cp -R "$HOME/projects/ai/llm_provider/$p" "$stage/$p"
done
```

- [ ] **Step 2: 將 staged imports 改回 agentSDK**

```bash
find "$stage" -name '*.go' -print0 | xargs -0 sed -i '' \
  -e 's#github.com/bizshuk/llm_provider/core#github.com/bizshuk/agentsdk/core#g' \
  -e 's#github.com/bizshuk/llm_provider/#github.com/bizshuk/agentsdk/provider/#g'
gofmt -w "$stage"
```

- [ ] **Step 3: 驗證 staged copy 無 llm_provider 殘留**

```bash
! grep -R "github.com/bizshuk/llm_provider" "$stage" --include='*.go'
```

Expected: exit 0、無輸出。

- [ ] **Step 4: 完整替換 agentSDK/provider**

```bash
rm -rf "$HOME/projects/agentSDK/provider"
mv "$stage" "$HOME/projects/agentSDK/provider"
```

- [ ] **Step 5: 驗證 provider packages**

```bash
cd "$HOME/projects/agentSDK"
go test ./provider/...
go build ./...
```

Expected: provider tests 與 agentSDK build 通過。

### Task 2: 將 proxy 重新接回 agentSDK

**Files:**
- Modify: `svc/upstream/dispatcher.go`
- Modify: `svc/upstream/dispatcher_default.go`
- Modify: `svc/upstream/dispatcher_oauth.go`
- Modify: `svc/upstream/dispatcher_test.go`
- Modify: `handlers/handler_dispatcher_test.go`
- Modify: `go.mod`
- Modify: `go.work`（gitignored）

**Interfaces:**
- Consumes: Task 1 產生的 `agentsdk/provider/*` 與既有 `agentsdk/core`。
- Produces: 不再 import llm_provider 的 proxy。

- [ ] **Step 1: 改寫五個 consumer imports**

```bash
files=(
  svc/upstream/dispatcher.go
  svc/upstream/dispatcher_default.go
  svc/upstream/dispatcher_oauth.go
  svc/upstream/dispatcher_test.go
  handlers/handler_dispatcher_test.go
)
sed -i '' \
  -e 's#github.com/bizshuk/llm_provider/core#github.com/bizshuk/agentsdk/core#g' \
  -e 's#github.com/bizshuk/llm_provider/#github.com/bizshuk/agentsdk/provider/#g' \
  "${files[@]}"
gofmt -w "${files[@]}"
```

- [ ] **Step 2: 恢復 proxy go.mod 的 agentSDK require/replace**

恢復以下內容：

```go
require (
    github.com/bizshuk/agentsdk v0.0.0-20260718201845-f1eecd0f1ed4
    // existing direct dependencies remain unchanged
)

replace github.com/bizshuk/agentsdk => ../../agentSDK
```

- [ ] **Step 3: 更新 proxy go.work**

```go
// go.work
go 1.26.0

use (
    .
    ../../agentSDK
)
```

- [ ] **Step 4: 驗證 proxy import graph**

```bash
! grep -R "github.com/bizshuk/llm_provider" . --include='*.go'
grep -R "github.com/bizshuk/agentsdk/provider" svc/upstream --include='*.go'
```

Expected: 第一條無輸出；第二條列出 dispatcher provider imports。

### Task 3: 修正 port 漂移測試

**Files:**
- Modify: `cmd/cmd_test.go:29-30`
- Test: `cmd/cmd_test.go`

**Interfaces:**
- Consumes: `cmd/cmd.go` 現有局部變數 `port`。
- Produces: 對齊現行 source shape 的 regression test。

- [ ] **Step 1: 先執行現有失敗測試**

```bash
cd "$HOME/projects/ai/proxy"
go test ./cmd -run TestCommandScopeKeepsPortFlagInline -count=1
```

Expected: FAIL，缺少 `&opts.port` 與 `cfg.Server.Port=opts.port`。

- [ ] **Step 2: 更新兩個字串斷言**

```go
assert.Contains(t, string(source), `PersistentFlags().IntVar(&port, "port", DEFAULT_PORT, "Server port")`)
assert.Contains(t, strings.ReplaceAll(string(source), " ", ""), "cfg.Server.Port=port")
```

- [ ] **Step 3: 執行單一測試**

```bash
go test ./cmd -run TestCommandScopeKeepsPortFlagInline -count=1
```

Expected: PASS。

### Task 4: 清理 workspace 與臨時模組

**Files:**
- Modify: `~/projects/agentSDK/go.work`
- Modify/generated: `~/projects/agentSDK/go.work.sum`
- Delete: `~/projects/ai/llm_provider/`

**Interfaces:**
- Consumes: Tasks 1–3 已通過的單一 agentSDK provider 架構。
- Produces: 不再依賴 llm_provider 的兩 repo workspace。

- [ ] **Step 1: 從 agentSDK/go.work 移除 llm_provider use**

僅刪除此行：

```go
../ai/llm_provider
```

保留 `../ai/proxy` 與其他既有 entries。

- [ ] **Step 2: 最後一次確認無 llm_provider import**

```bash
! grep -R "github.com/bizshuk/llm_provider" \
  "$HOME/projects/agentSDK" "$HOME/projects/ai/proxy" --include='*.go'
```

- [ ] **Step 3: 刪除臨時模組**

```bash
rm -rf "$HOME/projects/ai/llm_provider"
```

- [ ] **Step 4: 更新 workspace sums**

```bash
cd "$HOME/projects/agentSDK" && go work sync
```

### Task 5: 最終驗證與範圍審查

**Files:**
- Verify only: agentSDK、proxy 全部本次變更。

**Interfaces:**
- Consumes: 最終依賴 graph `proxy → agentsdk/provider → agentsdk/core`。
- Produces: 可交付、無 llm_provider 殘留的工作樹。

- [ ] **Step 1: 驗證 agentSDK**

```bash
cd "$HOME/projects/agentSDK"
go build ./...
go test ./provider/...
```

Expected: 全部通過。

- [ ] **Step 2: 驗證 proxy**

```bash
cd "$HOME/projects/ai/proxy"
go build ./...
go vet ./...
go test ./...
```

Expected: 全部通過，包含修正後的 cmd test。

- [ ] **Step 3: 抽驗新版 provider 內容**

```bash
grep -R 'gpt-5.6-sol\|gpt-5.6-terra\|gpt-5.6-luna' "$HOME/projects/agentSDK/provider/codex"
test -f "$HOME/projects/agentSDK/provider/google/auth_api.go"
test -f "$HOME/projects/agentSDK/provider/google/dto.go"
test -f "$HOME/projects/agentSDK/provider/google/stream.go"
test -f "$HOME/projects/agentSDK/provider/google/validate.go"
```

Expected: codex 三個模型皆存在，四個 google 完整版檔案皆存在。

- [ ] **Step 4: 審查未提交範圍**

```bash
git -C "$HOME/projects/agentSDK" status --short
git -C "$HOME/projects/ai/proxy" status --short
```

Expected: 本次只新增 agentSDK/provider、go.work/sum 與 proxy provider import/go.mod/cmd test 相關差異；其他既有未提交變更保持不變。

## Self-Review

- Spec coverage：完整 provider 替換、proxy reconnect、port test、workspace cleanup、刪除 llm_provider、三層驗證皆有對應 task。
- Placeholder scan：無 TBD/TODO/模糊實作步驟。
- Type consistency：provider 與 proxy 均統一使用 `github.com/bizshuk/agentsdk/core`；provider import path 統一為 `github.com/bizshuk/agentsdk/provider/<name>`。
- Commit policy：本計畫無 commit step，符合使用者未授權 commit 的限制。
