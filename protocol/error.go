package protocol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ErrorKind classifies failures independently of a client wire format.
type ErrorKind string

const (
	ERROR_INVALID_REQUEST     ErrorKind = "invalid_request"
	ERROR_UNKNOWN_MODEL       ErrorKind = "unknown_model"
	ERROR_UNSUPPORTED_FEATURE ErrorKind = "unsupported_feature"
	ERROR_AUTH                ErrorKind = "auth"
	ERROR_RATE_LIMIT          ErrorKind = "rate_limit"
	ERROR_UPSTREAM            ErrorKind = "upstream"
	ERROR_UNAVAILABLE         ErrorKind = "unavailable"
	ERROR_TIMEOUT             ErrorKind = "timeout"
	ERROR_PROTOCOL            ErrorKind = "protocol"
)

// ProxyError is the protocol-neutral error exchanged between proxy layers.
type ProxyError struct {
	Kind              ErrorKind
	Status            int
	Code              string
	Message           string
	RetryAfter        time.Duration
	UpstreamRequestID string
	Cause             error
}

// Error implements error.
func (e *ProxyError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return string(e.Kind)
}

// Unwrap exposes the underlying implementation error without serializing it.
func (e *ProxyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// StatusCode returns the explicit status or the default for this error kind.
func (e *ProxyError) StatusCode() int {
	if e != nil && e.Status > 0 {
		return e.Status
	}
	if e == nil {
		return http.StatusInternalServerError
	}
	switch e.Kind {
	case ERROR_INVALID_REQUEST, ERROR_UNKNOWN_MODEL, ERROR_UNSUPPORTED_FEATURE:
		return http.StatusBadRequest
	case ERROR_AUTH:
		return http.StatusUnauthorized
	case ERROR_RATE_LIMIT:
		return http.StatusTooManyRequests
	case ERROR_UNAVAILABLE:
		return http.StatusServiceUnavailable
	case ERROR_TIMEOUT:
		return http.StatusGatewayTimeout
	case ERROR_UPSTREAM, ERROR_PROTOCOL:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// EncodeError serializes a safe error in the source protocol's public shape.
func EncodeError(format Format, proxyErr *ProxyError) ([]byte, error) {
	if proxyErr == nil {
		return nil, fmt.Errorf("encode proxy error: nil error")
	}
	message := proxyErr.Message
	if message == "" {
		message = http.StatusText(proxyErr.StatusCode())
	}
	code := proxyErr.Code
	if code == "" {
		code = string(proxyErr.Kind)
	}
	errorType := publicErrorType(proxyErr.Kind)

	var payload any
	switch format {
	case FORMAT_ANTHROPIC_MESSAGES:
		payload = struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}{Type: "error"}
		value := payload.(struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		})
		value.Error.Type = errorType
		value.Error.Message = message
		payload = value
	case FORMAT_OPENAI_CHAT, FORMAT_OPENAI_RESPONSES:
		payload = struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}{}
		value := payload.(struct {
			Error struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		})
		value.Error.Type = errorType
		value.Error.Code = code
		value.Error.Message = message
		payload = value
	default:
		return nil, fmt.Errorf("encode proxy error: unknown format %q", format)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode proxy error: %w", err)
	}
	return body, nil
}

func publicErrorType(kind ErrorKind) string {
	switch kind {
	case ERROR_INVALID_REQUEST, ERROR_UNKNOWN_MODEL, ERROR_UNSUPPORTED_FEATURE:
		return "invalid_request_error"
	case ERROR_AUTH:
		return "authentication_error"
	case ERROR_RATE_LIMIT:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}
