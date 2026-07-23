package transform

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bizshuk/proxy/model"
)

// upstreamErrorProxyError parses an upstream SSE error frame (Anthropic
// `event: error` or Responses `response.failed`) and returns a
// *model.ProxyError that carries the upstream code/message/type so
// the public wire error and the DEBUG req.failed snapshot both
// surface the real failure reason.
func upstreamErrorProxyError(data []byte) error {
	var payload struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Response struct {
			Status string `json:"status"`
			Error  struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return protocolFailure(fmt.Errorf("decode upstream error frame: %w", err))
	}
	proxyErr := &model.ProxyError{
		Kind:    model.ERROR_UPSTREAM,
		Status:  http.StatusBadGateway,
		Message: payload.Error.Message,
		Cause:   fmt.Errorf("upstream stream failed"),
	}
	if payload.Error.Code != "" {
		proxyErr.Code = payload.Error.Code
		proxyErr.UpstreamErrorCode = payload.Error.Code
	} else if payload.Response.Error.Code != "" {
		proxyErr.Code = payload.Response.Error.Code
		proxyErr.UpstreamErrorCode = payload.Response.Error.Code
	} else {
		proxyErr.Code = "upstream_error"
		proxyErr.UpstreamErrorCode = "upstream_error"
	}
	if payload.Error.Type != "" {
		proxyErr.UpstreamErrorType = payload.Error.Type
	} else if payload.Type != "" {
		proxyErr.UpstreamErrorType = payload.Type
	}
	if payload.Error.Message != "" {
		proxyErr.UpstreamErrorMessage = payload.Error.Message
	} else if payload.Response.Error.Message != "" {
		proxyErr.UpstreamErrorMessage = payload.Response.Error.Message
	}
	if proxyErr.Message == "" && proxyErr.UpstreamErrorMessage != "" {
		proxyErr.Message = proxyErr.UpstreamErrorMessage
	}
	if proxyErr.Message == "" {
		proxyErr.Message = "upstream stream failed"
	}
	return proxyErr
}
