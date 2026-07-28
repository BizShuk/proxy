package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
)

const (
	REALTIME_TRANSPORT_WEBSOCKET = "websocket"
	REALTIME_TRANSPORT_WEBRTC    = "webrtc"
)

var realtimeTransportHeaders = []string{
	"Accept",
	"Content-Type",
	"Connection",
	"Upgrade",
	"Sec-WebSocket-Key",
	"Sec-WebSocket-Version",
	"Sec-WebSocket-Protocol",
	"Sec-WebSocket-Extensions",
}

// RealtimeHandlerDeps contains the immutable dependencies for Realtime session
// initialization and WebSocket proxying.
type RealtimeHandlerDeps struct {
	Catalog           *upstream.Catalog
	Credentials       *upstream.CredentialResolver
	MaxConnections    int
	MaxHandshakeBytes int64
}

// RealtimeHandler proxies OpenAI-native Realtime transports without inspecting
// or translating event and audio payloads.
type RealtimeHandler struct {
	catalog           *upstream.Catalog
	credentials       *upstream.CredentialResolver
	connectionSlots   chan struct{}
	maxHandshakeBytes int64
}

// NewRealtimeHandler validates and constructs the Realtime transport handler.
func NewRealtimeHandler(deps RealtimeHandlerDeps) (*RealtimeHandler, error) {
	switch {
	case deps.Catalog == nil:
		return nil, fmt.Errorf("new realtime handler: catalog is required")
	case deps.Credentials == nil:
		return nil, fmt.Errorf("new realtime handler: credential resolver is required")
	case deps.MaxConnections <= 0:
		return nil, fmt.Errorf("new realtime handler: max connections must be positive")
	case deps.MaxHandshakeBytes <= 0:
		return nil, fmt.Errorf("new realtime handler: max handshake bytes must be positive")
	}
	return &RealtimeHandler{
		catalog:           deps.Catalog,
		credentials:       deps.Credentials,
		connectionSlots:   make(chan struct{}, deps.MaxConnections),
		maxHandshakeBytes: deps.MaxHandshakeBytes,
	}, nil
}

// HandleWebSocket upgrades and transparently tunnels an OpenAI Realtime event
// session. Audio and JSON events remain opaque to the proxy.
func (handler *RealtimeHandler) HandleWebSocket() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket") {
			writeRealtimeError(c, http.StatusUpgradeRequired, "websocket_upgrade_required", "Realtime WebSocket endpoint requires an Upgrade request")
			return
		}
		if strings.TrimSpace(c.Query("model")) == "" {
			writeRealtimeError(c, http.StatusBadRequest, "model_required", "Realtime WebSocket endpoint requires a model query parameter")
			return
		}
		select {
		case handler.connectionSlots <- struct{}{}:
			defer func() { <-handler.connectionSlots }()
		default:
			writeRealtimeError(c, http.StatusTooManyRequests, "realtime_connection_limit", "Realtime connection limit reached")
			return
		}
		handler.proxy(c, upstream.OPENAI_REALTIME_WEBSOCKET_ENDPOINT, REALTIME_TRANSPORT_WEBSOCKET)
	}
}

// HandleHandshake proxies one bounded Realtime WebRTC call or ephemeral-secret
// request using the OpenAI-compatible request body.
func (handler *RealtimeHandler) HandleHandshake(endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := handler.bufferHandshakeBody(c); err != nil {
			writeRealtimeFailure(c, err)
			return
		}
		handler.proxy(c, endpoint, REALTIME_TRANSPORT_WEBRTC)
	}
}

func (handler *RealtimeHandler) bufferHandshakeBody(c *gin.Context) error {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, handler.maxHandshakeBytes+1))
	if err != nil {
		return &model.ProxyError{
			Kind:    model.ERROR_INVALID_REQUEST,
			Status:  http.StatusBadRequest,
			Code:    "invalid_realtime_handshake",
			Message: "read Realtime handshake body failed",
			Cause:   err,
		}
	}
	if int64(len(body)) > handler.maxHandshakeBytes {
		return &model.ProxyError{
			Kind:    model.ERROR_INVALID_REQUEST,
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "realtime_handshake_too_large",
			Message: "Realtime handshake body exceeds configured limit",
		}
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	return nil
}

func (handler *RealtimeHandler) proxy(c *gin.Context, endpoint, transport string) {
	reqID := requestID(c.GetHeader("x-request-id"))
	credential, err := handler.credentials.Resolve(c.Request.Context(), "openai")
	if err != nil {
		writeRealtimeFailure(c, err)
		return
	}
	target, err := upstream.PrepareOpenAIRealtimeTarget(handler.catalog, credential, endpoint, c.Request.Header)
	if err != nil {
		writeRealtimeFailure(c, err)
		return
	}
	target.RequestHeaders.Set("x-request-id", reqID)

	startedAt := time.Now()
	slog.Info("proxy realtime session routed",
		"request_id", reqID,
		"provider", "openai",
		"transport", transport,
		"endpoint", endpoint,
		"model", strings.TrimSpace(c.Query("model")),
		"has_safety_identifier", strings.TrimSpace(c.GetHeader(upstream.OPENAI_SAFETY_IDENTIFIER_HEADER)) != "",
	)

	proxy := handler.reverseProxy(c, target, reqID, endpoint, transport)
	proxy.ServeHTTP(c.Writer, c.Request)

	slog.Info("proxy realtime session completed",
		"request_id", reqID,
		"provider", "openai",
		"transport", transport,
		"endpoint", endpoint,
		"status", c.Writer.Status(),
		"duration_ms", time.Since(startedAt).Milliseconds(),
	)
}

func (handler *RealtimeHandler) reverseProxy(
	c *gin.Context,
	target upstream.RealtimeTarget,
	reqID string,
	endpoint string,
	transport string,
) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = target.URL.Scheme
			request.Out.URL.Host = target.URL.Host
			request.Out.URL.Path = target.URL.Path
			request.Out.URL.RawPath = ""
			request.Out.URL.RawQuery = request.In.URL.RawQuery
			request.Out.URL.ForceQuery = request.In.URL.ForceQuery
			request.Out.Host = target.URL.Host

			headers := target.RequestHeaders.Clone()
			copyRealtimeTransportHeaders(headers, request.In.Header)
			request.Out.Header = headers
		},
		ModifyResponse: func(response *http.Response) error {
			if response.StatusCode == http.StatusSwitchingProtocols {
				response.Header.Del("Set-Cookie")
				response.Header.Del("Proxy-Authenticate")
				return nil
			}
			response.Header = target.SanitizeResponseHeaders(response.Header)
			if response.StatusCode >= http.StatusBadRequest {
				slog.Error("proxy realtime upstream response",
					"request_id", reqID,
					"provider", "openai",
					"transport", transport,
					"endpoint", endpoint,
					"status", response.StatusCode,
				)
			}
			return nil
		},
		ErrorHandler: func(_ http.ResponseWriter, _ *http.Request, err error) {
			slog.Error("proxy realtime upstream failed",
				"request_id", reqID,
				"provider", "openai",
				"transport", transport,
				"endpoint", endpoint,
				"error", err.Error(),
			)
			if !c.Writer.Written() {
				writeRealtimeError(c, http.StatusBadGateway, "realtime_upstream_error", "OpenAI Realtime upstream request failed")
			}
		},
	}
}

func copyRealtimeTransportHeaders(target, source http.Header) {
	for _, name := range realtimeTransportHeaders {
		for _, value := range source.Values(name) {
			target.Add(name, value)
		}
	}
}

func writeRealtimeFailure(c *gin.Context, err error) {
	status := http.StatusServiceUnavailable
	code := "realtime_unavailable"
	message := "Realtime request is unavailable"
	var proxyError *model.ProxyError
	if errors.As(err, &proxyError) {
		if proxyError.Status > 0 {
			status = proxyError.Status
		}
		if strings.TrimSpace(proxyError.Code) != "" {
			code = proxyError.Code
		}
		if strings.TrimSpace(proxyError.Message) != "" {
			message = proxyError.Message
		}
	}
	writeRealtimeError(c, status, code, message)
}

func writeRealtimeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    "api_error",
			"code":    code,
			"message": message,
		},
	})
}
