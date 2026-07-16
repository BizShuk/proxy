package transform

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bizshuk/proxy/protocol"
)

const MAX_UPSTREAM_ERROR_MESSAGE_BYTES = 512

// DecodeUpstreamError converts an upstream HTTP failure into a safe proxy error.
func DecodeUpstreamError(status int, headers http.Header, body []byte) *protocol.ProxyError {
	redirect := status >= http.StatusMultipleChoices && status < http.StatusBadRequest
	kind := upstreamErrorKind(status)
	if redirect {
		status = http.StatusBadGateway
		kind = protocol.ERROR_UPSTREAM
	}
	proxyErr := &protocol.ProxyError{
		Kind:    kind,
		Status:  status,
		Code:    "upstream_error",
		Message: "upstream request failed",
	}
	if headers != nil {
		proxyErr.RetryAfter = parseRetryAfter(headerValueCI(headers, "Retry-After"), time.Now())
		for _, name := range []string{"x-request-id", "request-id", "cf-ray"} {
			if value := strings.TrimSpace(headerValueCI(headers, name)); value != "" {
				proxyErr.UpstreamRequestID = value
				break
			}
		}
	}
	if redirect {
		return proxyErr
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		message := payload.Error.Message
		if message == "" {
			message = payload.Message
		}
		if strings.TrimSpace(message) != "" {
			proxyErr.Message = truncateUTF8(strings.TrimSpace(message), MAX_UPSTREAM_ERROR_MESSAGE_BYTES)
		}
		code := payload.Error.Code
		if code == "" {
			code = payload.Code
		}
		if strings.TrimSpace(code) != "" {
			proxyErr.Code = truncateUTF8(strings.TrimSpace(code), 128)
		}
	}
	return proxyErr
}

func headerValueCI(headers http.Header, name string) string {
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func upstreamErrorKind(status int) protocol.ErrorKind {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return protocol.ERROR_AUTH
	case http.StatusTooManyRequests:
		return protocol.ERROR_RATE_LIMIT
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return protocol.ERROR_TIMEOUT
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return protocol.ERROR_UNAVAILABLE
	default:
		return protocol.ERROR_UPSTREAM
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func anthropicToChatStop(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "stop_sequence", "end_turn", "pause_turn", "refusal", "":
		return "stop"
	default:
		return "stop"
	}
}

func chatToAnthropicStop(reason string) string {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func responseModel(envelope protocol.ResponseEnvelope, fallback string) string {
	if model := strings.TrimSpace(envelope.Exchange.OriginalRequest.Model); model != "" {
		return model
	}
	return fallback
}

func generatedID(exchange protocol.Exchange, fallback string) string {
	if exchange.NewID != nil {
		return exchange.NewID()
	}
	return fallback
}

func validateArguments(arguments string) (json.RawMessage, error) {
	if strings.TrimSpace(arguments) == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		return nil, protocolFailure(fmt.Errorf("tool arguments are not valid JSON"))
	}
	return json.RawMessage(arguments), nil
}

func cachedTokenLoss(creationTokens int) []protocol.SemanticLoss {
	if creationTokens == 0 {
		return nil
	}
	return []protocol.SemanticLoss{{
		Field:  "usage.cache_tokens",
		Reason: "target usage has one cached-token bucket and cannot distinguish cache creation",
	}}
}
