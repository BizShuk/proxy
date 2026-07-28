package handlers

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
)

// RealtimeHandlerDeps defines dependencies for RealtimeHandler.
type RealtimeHandlerDeps struct {
	Catalog           *upstream.Catalog
	Credentials       *upstream.CredentialResolver
	MaxConnections    int
	MaxHandshakeBytes int64
}

// RealtimeHandler handles OpenAI Realtime WebSocket relay and WebRTC handshake endpoints.
type RealtimeHandler struct {
	deps        RealtimeHandlerDeps
	activeConns atomic.Int64
	client      *http.Client
}

// NewRealtimeHandler constructs a RealtimeHandler.
func NewRealtimeHandler(deps RealtimeHandlerDeps) (*RealtimeHandler, error) {
	if deps.Credentials == nil {
		return nil, fmt.Errorf("new realtime handler: credentials resolver is required")
	}
	if deps.MaxConnections <= 0 {
		deps.MaxConnections = 32
	}
	if deps.MaxHandshakeBytes <= 0 {
		deps.MaxHandshakeBytes = 1 << 20
	}
	return &RealtimeHandler{
		deps: deps,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// HandleWebSocket relays WebSocket connection to OpenAI Realtime endpoint.
func (h *RealtimeHandler) HandleWebSocket() gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := requestID(c.GetHeader("x-request-id"))

		if h.deps.MaxConnections > 0 && h.activeConns.Load() >= int64(h.deps.MaxConnections) {
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_RATE_LIMIT,
				Status:  http.StatusTooManyRequests,
				Code:    "rate_limit_exceeded",
				Message: "realtime connection limit reached",
			})
			return
		}

		cred, err := h.deps.Credentials.Resolve(c.Request.Context(), "openai")
		if err != nil {
			h.writeProxyError(c, err)
			return
		}
		secret := credentialSecret(cred)

		targetURL := upstream.OPENAI_REALTIME_WEBSOCKET_ENDPOINT
		parsedURL, err := url.Parse(targetURL)
		if err != nil {
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_PROTOCOL,
				Status:  http.StatusInternalServerError,
				Code:    "internal_error",
				Message: "invalid realtime upstream endpoint",
				Cause:   err,
			})
			return
		}

		host := parsedURL.Host
		if !strings.Contains(host, ":") {
			host = host + ":443"
		}

		rawPath := parsedURL.Path
		if rawPath == "" {
			rawPath = "/v1/realtime"
		}
		if c.Request.URL.RawQuery != "" {
			rawPath += "?" + c.Request.URL.RawQuery
		}

		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: 10 * time.Second},
		}
		upstreamConn, err := dialer.DialContext(c.Request.Context(), "tcp", host)
		if err != nil {
			slog.Error("proxy realtime upstream failed", slog.String("request_id", reqID), slog.Any("error", err))
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_UPSTREAM,
				Status:  http.StatusBadGateway,
				Code:    "upstream_connect_failed",
				Message: "failed to connect to upstream realtime server",
				Cause:   err,
			})
			return
		}
		defer upstreamConn.Close()

		secKey := c.GetHeader("Sec-WebSocket-Key")
		reqHeader := fmt.Sprintf("GET %s HTTP/1.1\r\n"+
			"Host: %s\r\n"+
			"Authorization: Bearer %s\r\n"+
			"OpenAI-Beta: realtime=v1\r\n"+
			"Upgrade: websocket\r\n"+
			"Connection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\n"+
			"Sec-WebSocket-Version: 13\r\n\r\n",
			rawPath, parsedURL.Hostname(), secret, secKey)

		if _, err := upstreamConn.Write([]byte(reqHeader)); err != nil {
			slog.Error("proxy realtime upstream failed", slog.String("request_id", reqID), slog.Any("error", err))
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_UPSTREAM,
				Status:  http.StatusBadGateway,
				Code:    "upstream_write_failed",
				Message: "failed to write request to upstream realtime server",
				Cause:   err,
			})
			return
		}

		hj, ok := c.Writer.(http.Hijacker)
		if !ok {
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_PROTOCOL,
				Status:  http.StatusInternalServerError,
				Code:    "hijack_not_supported",
				Message: "web server does not support connection hijacking",
			})
			return
		}

		clientConn, buf, err := hj.Hijack()
		if err != nil {
			slog.Error("proxy realtime hijack failed", slog.String("request_id", reqID), slog.Any("error", err))
			return
		}
		defer clientConn.Close()

		h.activeConns.Add(1)
		defer h.activeConns.Add(-1)

		slog.Info("proxy realtime session routed", slog.String("request_id", reqID), slog.String("model", c.Query("model")))

		if buf.Reader.Buffered() > 0 {
			buffered, _ := buf.Reader.Peek(buf.Reader.Buffered())
			_, _ = upstreamConn.Write(buffered)
		}

		errc := make(chan error, 2)
		go func() {
			_, err := io.Copy(upstreamConn, clientConn)
			errc <- err
		}()
		go func() {
			_, err := io.Copy(clientConn, upstreamConn)
			errc <- err
		}()
		<-errc

		slog.Info("proxy realtime session completed", slog.String("request_id", reqID))
	}
}

// HandleHandshake proxies WebRTC call and client secret handshake requests.
func (h *RealtimeHandler) HandleHandshake(endpoint string) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqID := requestID(c.GetHeader("x-request-id"))

		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.deps.MaxHandshakeBytes)

		cred, err := h.deps.Credentials.Resolve(c.Request.Context(), "openai")
		if err != nil {
			h.writeProxyError(c, err)
			return
		}
		secret := credentialSecret(cred)

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_INVALID_REQUEST,
				Status:  http.StatusBadRequest,
				Code:    "invalid_request_body",
				Message: "failed to read handshake request body",
				Cause:   err,
			})
			return
		}

		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_PROTOCOL,
				Status:  http.StatusInternalServerError,
				Code:    "internal_error",
				Message: "failed to create upstream handshake request",
				Cause:   err,
			})
			return
		}

		ct := c.GetHeader("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("OpenAI-Beta", "realtime=v1")

		resp, err := h.client.Do(req)
		if err != nil {
			slog.Error("proxy realtime upstream failed", slog.String("request_id", reqID), slog.Any("error", err))
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_UPSTREAM,
				Status:  http.StatusBadGateway,
				Code:    "upstream_request_failed",
				Message: "upstream realtime handshake failed",
				Cause:   err,
			})
			return
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			slog.Error("proxy realtime upstream failed", slog.String("request_id", reqID), slog.Any("error", err))
			h.writeProxyError(c, &model.ProxyError{
				Kind:    model.ERROR_UPSTREAM,
				Status:  http.StatusBadGateway,
				Code:    "upstream_read_failed",
				Message: "failed to read upstream handshake response",
				Cause:   err,
			})
			return
		}

		if resp.StatusCode >= 400 {
			slog.Error("proxy realtime upstream failed", slog.String("request_id", reqID), slog.Int("status_code", resp.StatusCode))
		}

		if respCT := resp.Header.Get("Content-Type"); respCT != "" {
			c.Header("Content-Type", respCT)
		}
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
	}
}

func credentialSecret(cred *upstream.ResolvedCredential) string {
	if cred == nil {
		return ""
	}
	if cred.Kind == authmodel.KIND_OAUTH {
		return cred.AccessToken
	}
	return cred.APIKey
}

func (h *RealtimeHandler) writeProxyError(c *gin.Context, err error) {
	proxyErr := asProxyError(err)
	body, encodeErr := model.EncodeError(model.FORMAT_OPENAI_CHAT, proxyErr)
	if encodeErr != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(proxyErr.StatusCode(), "application/json", body)
}
