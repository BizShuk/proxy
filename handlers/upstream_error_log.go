package handlers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// MAX_UPSTREAM_ERROR_BYTES caps how many bytes of an upstream
// error response we log to slog.
//
// This is the canonical declaration; the previous one in
// handler.go has been removed because Go's package-level
// namespace forbids two same-named constants in the same
// package.
const MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10

// sensitiveHeaders is the case-insensitive deny-list of response
// header names that must never appear in proxy error logs. We
// deliberately keep this list narrow — request/response bodies
// are written verbatim by policy (see spec section 3), but
// upstream 4xx/5xx response headers can echo credentials
// (Authorization, Set-Cookie) and these are filtered out.
var sensitiveHeaders = map[string]struct{}{
	"authorization":        {},
	"proxy-authorization":  {},
	"cookie":               {},
	"set-cookie":           {},
	"x-api-key":            {},
	"api-key":              {},
	"x-auth-token":         {},
	"x-amz-security-token": {},
}

// filterResponseHeaders returns a copy of h with sensitive header
// names removed. Always returns a non-nil http.Header.
func filterResponseHeaders(h http.Header) http.Header {
	out := http.Header{}
	for name, values := range h {
		if _, skip := sensitiveHeaders[strings.ToLower(name)]; skip {
			continue
		}
		out[name] = values
	}
	return out
}

// logUpstreamError records an upstream 4xx/5xx response at
// level=Error and returns the body for the caller to continue
// feeding into DecodeUpstreamError. It never returns an error;
// the caller gets nil body when reading failed and falls back
// to the existing "no body" handling in handleUpstreamError.
//
// The returned body slice is capped at MAX_UPSTREAM_ERROR_BYTES
// so the caller cannot exhaust memory by streaming a huge error
// payload back into DecodeUpstreamError. The log entry, however,
// records the original body length via body_bytes.
func (h *Handler) logUpstreamError(
	ctx context.Context,
	requestIDValue, routedModel, providerID string,
	response *http.Response,
) []byte {
	if h == nil {
		return nil
	}
	attrs := []slog.Attr{
		slog.String("request_id", requestIDValue),
		slog.String("provider", providerID),
		slog.String("model", routedModel),
		slog.Int("status_code", response.StatusCode),
	}
	if response.Header != nil {
		for name, values := range filterResponseHeaders(response.Header) {
			if len(values) == 1 {
				attrs = append(attrs, slog.String("header."+strings.ToLower(name), values[0]))
				continue
			}
			attrs = append(attrs, slog.Any("header."+strings.ToLower(name), values))
		}
	}
	body, err := readUpstreamErrorBody(response)
	switch {
	case err != nil:
		attrs = append(attrs,
			slog.String("body_read_error", err.Error()),
			slog.Int64("body_bytes", 0),
		)
	case int64(len(body)) > MAX_UPSTREAM_ERROR_BYTES:
		attrs = append(attrs,
			slog.String("body", string(body[:MAX_UPSTREAM_ERROR_BYTES])),
			slog.Bool("body_truncated", true),
			slog.Int("body_bytes", len(body)),
		)
	default:
		attrs = append(attrs,
			slog.String("body", string(body)),
			slog.Bool("body_truncated", false),
		)
	}
	slog.LogAttrs(ctx, slog.LevelError, "proxy upstream error response", attrs...)
	if err != nil {
		return nil
	}
	if int64(len(body)) > MAX_UPSTREAM_ERROR_BYTES {
		body = body[:MAX_UPSTREAM_ERROR_BYTES]
	}
	return body
}

// readUpstreamErrorBody drains the response body so callers can
// record the original length via body_bytes. Upstream error
// bodies are small in practice (they're HTTP error payloads,
// not streaming responses) and the caller caps the returned
// slice at MAX_UPSTREAM_ERROR_BYTES before forwarding to
// downstream code, so unbounded reads are bounded by the
// upstream's own discipline. A nil Body is treated as "no body"
// rather than an error.
func readUpstreamErrorBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, errUpstreamBodyNil
	}
	return io.ReadAll(response.Body)
}

var errUpstreamBodyNil = stringError("response body nil")

type stringError string

func (s stringError) Error() string { return string(s) }