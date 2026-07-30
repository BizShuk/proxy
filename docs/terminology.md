# 術語表 (Terminology)

## 圖片生成 (Image Generation)

| 術語 (Term) | 英文 (English) | 定義 (Definition) | 出處 (Source) |
| --- | --- | --- | --- |
| 圖片生成端點 | Image Generations endpoint | Proxy 對外提供的 OpenAI-compatible `POST /v1/images/generations` HTTP 介面 | `handlers/server.go`、`handlers/image_generation.go` |
| 圖片 MCP 伺服器 | Image MCP server | 以 `stdio` 暴露 proxy 圖片生成能力，供 Codex 與 Claude Code 呼叫的本機程序 | `mcpimage/server.go:NewServer`、`cmd/image_mcp.go:ImageMCPCmd` |
| `generate_image` | Generate image tool | 接受提示詞與圖片選項、呼叫 proxy、儲存圖片並回傳 MCP image content 與路徑的工具 | `mcpimage/server.go:TOOL_NAME_GENERATE_IMAGE`、`mcpimage/tool.go:Generator.Generate` |
| 圖片輸出目錄 | Image output directory | 生成圖片的儲存位置；相對路徑必須位於 MCP client 的目前專案內 | `mcpimage/config.go:ENV_OUTPUT_DIR`、`mcpimage/tool.go:resolveOutputDir` |
| Proxy 圖片 API key | Proxy image API key | MCP server 呼叫本 proxy 時放入 `Authorization: Bearer` 的 downstream API key，不是直接交給上游 provider 的 credential | `mcpimage/config.go:ENV_API_KEY`、`mcpimage/client.go:ProxyClient.Generate` |

## 圖片編輯 (Image Edit)

| 術語 (Term) | 英文 (English) | 定義 (Definition) | 出處 (Source) |
| --- | --- | --- | --- |
| 圖片編輯端點 | Image Edits endpoint | Proxy 對外提供的 OpenAI-compatible `POST /v1/images/edits` multipart HTTP 介面 | `handlers/server.go`、`handlers/image_edit.go` |
| Personal Image | Personal image | downstream 上傳、只在 request lifecycle 內轉送給 Image Edit provider 的 JPEG/PNG/WebP image part | `handlers/image_edit.go`、`svc/upstream/client.go` |

## 縮寫 (Abbreviations)

| 縮寫 | 全稱 | 說明 |
| --- | --- | --- |
| MCP | Model Context Protocol | Codex 與 Claude Code 用來發現及呼叫 `generate_image` 的工具協定；實作依賴見 `go.mod` 的 `github.com/modelcontextprotocol/go-sdk` |
| MIME | Multipurpose Internet Mail Extensions | 用來辨識 Base64 解碼結果的圖片媒體類型；檢查點見 `mcpimage/client.go:imageMIMEType` |
