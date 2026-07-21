package handlers

import (
	"context"
	"log/slog"

	"github.com/bizshuk/proxy/model"
)

// DEBUG_PAYLOAD_MAX_BYTES caps the body bytes emitted per before/now
// snapshot. Larger payloads are truncated to this limit and carry
// body_truncated=true plus the original body_bytes count. The cap
// matches MAX_UPSTREAM_ERROR_BYTES so an operator reading the log
// can correlate error bodies and debug snapshots at a glance.
const DEBUG_PAYLOAD_MAX_BYTES = 64 << 10

// Debug stage labels. Use the constants below rather than free-form
// strings so log scrapers can rely on a fixed vocabulary.
const (
	debugStageRequestBefore  = "req.before"
	debugStageRequestNow     = "req.now"
	debugStageRequestFailed  = "req.failed"
	debugStageResponseBefore = "resp.before"
	debugStageResponseNow    = "resp.now"
)

// emitDebugPayload writes a single Debug-level slog record capturing
// the wire body at one of the four proxy stages. The record is emitted
// unconditionally; the slog handler's level filter (controlled via
// LOG_LEVEL=debug) decides whether it reaches the writer.
//
// The body field is the raw, un-redacted payload. Sensitive header
// values (authorization, x-api-key, …) never enter this log path
// because we log the parsed request body, not the headers. Operators
// running with LOG_LEVEL=debug accept the responsibility of redacting
// logs downstream.
func (h *Handler) emitDebugPayload(
	ctx context.Context,
	stage string,
	requestIDValue, routedModel, providerID string,
	sourceFormat, targetFormat model.Format,
	body []byte,
) {
	if h == nil {
		return
	}
	capped, truncated := truncateBytes(body, DEBUG_PAYLOAD_MAX_BYTES)
	slog.LogAttrs(ctx, slog.LevelDebug, "proxy debug payload",
		slog.String("stage", stage),
		slog.String("request_id", requestIDValue),
		slog.String("model", routedModel),
		slog.String("provider", providerID),
		slog.String("source_format", string(sourceFormat)),
		slog.String("target_format", string(targetFormat)),
		slog.Int("body_bytes", len(body)),
		slog.Bool("body_truncated", truncated),
		slog.String("body", string(capped)),
	)
}

// truncateBytes returns the first limit bytes of body along with a
// truncated flag. Negative or zero limits degrade to "no body" with
// truncated=true so the operator sees that the snapshot is empty.
func truncateBytes(body []byte, limit int) ([]byte, bool) {
	if limit <= 0 {
		return nil, true
	}
	if len(body) <= limit {
		return body, false
	}
	return body[:limit], true
}

// emitDebugFailure logs a Debug-level record capturing the request
// failure context — fired from any writeError site that bails out
// before the request reaches client.Do. The chain then reads:
//
//	DEBUG proxy debug payload stage=req.failed error_code=... error_kind=...
//
// so operators can correlate a 400/422 with the exact step that
// rejected the request, even when no upstream body is available.
//
// body is optional; pass nil when no body is yet associated with the
// request (e.g. router.Resolve failed before reading the body).
func (h *Handler) emitDebugFailure(
	ctx context.Context,
	requestIDValue, routedModel, providerID string,
	sourceFormat, targetFormat model.Format,
	errorCode, errorKind, errorMessage string,
	body []byte,
) {
	if h == nil {
		return
	}
	capped, truncated := truncateBytes(body, DEBUG_PAYLOAD_MAX_BYTES)
	slog.LogAttrs(ctx, slog.LevelDebug, "proxy debug payload",
		slog.String("stage", debugStageRequestFailed),
		slog.String("request_id", requestIDValue),
		slog.String("model", routedModel),
		slog.String("provider", providerID),
		slog.String("source_format", string(sourceFormat)),
		slog.String("target_format", string(targetFormat)),
		slog.String("error_code", errorCode),
		slog.String("error_kind", errorKind),
		slog.String("error_message", errorMessage),
		slog.Int("body_bytes", len(body)),
		slog.Bool("body_truncated", truncated),
		slog.String("body", string(capped)),
	)
}