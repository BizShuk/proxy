package handlers

import (
	"net/http"
	"strings"
)

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
