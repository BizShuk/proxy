# 錯誤回應 (Error Response)

`案例`：HTTP `429 Too Many Requests`。

## 1. Provider send

`status`：`429`

```json
{
  "type": "error",
  "error": {
    "type": "rate_limit_error",
    "message": "rate limited"
  }
}
```

## 2. Client receive

`status`：`429`

```json
{
  "error": {
    "type": "rate_limit_error",
    "code": "rate_limit",
    "message": "rate limited"
  }
}
```

## Mapping rules

- provider 的 secret/header/body 不直接透傳；先 decode 成安全的 proxy error，再依 client format encode。
- `Retry-After` 與 request ID 可作為安全 response header 保留，但不放進範例 JSON。
- HTML 或未知 upstream body 不回顯給 client，避免洩漏 credential/prompt。

