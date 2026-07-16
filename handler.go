package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/route"
	"github.com/bizshuk/proxy/transform"
	"github.com/bizshuk/proxy/upstream"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const MAX_UPSTREAM_ERROR_BYTES int64 = 64 << 10

var errUpstreamResponseTooLarge = errors.New("upstream response exceeds limit")

// Handler coordinates routing, protocol transforms, credentials, and upstream I/O.
type Handler struct {
	router       *route.Router
	registry     *transform.Registry
	catalog      *upstream.Catalog
	credentials  *upstream.CredentialResolver
	client       *upstream.Client
	observer     TransformObserver
	maxBodyBytes int64
}

// HandlerDeps contains the immutable dependencies shared by proxy requests.
type HandlerDeps struct {
	Router       *route.Router
	Registry     *transform.Registry
	Catalog      *upstream.Catalog
	Credentials  *upstream.CredentialResolver
	Client       *upstream.Client
	Observer     TransformObserver
	MaxBodyBytes int64
}

// NewHandler validates and constructs a generic protocol handler.
func NewHandler(deps HandlerDeps) (*Handler, error) {
	switch {
	case deps.Router == nil:
		return nil, fmt.Errorf("new proxy handler: router is required")
	case deps.Registry == nil:
		return nil, fmt.Errorf("new proxy handler: transform registry is required")
	case deps.Catalog == nil:
		return nil, fmt.Errorf("new proxy handler: upstream catalog is required")
	case deps.Credentials == nil:
		return nil, fmt.Errorf("new proxy handler: credential resolver is required")
	case deps.Client == nil:
		return nil, fmt.Errorf("new proxy handler: upstream client is required")
	case deps.Observer == nil:
		return nil, fmt.Errorf("new proxy handler: transform observer is required")
	case deps.MaxBodyBytes <= 0:
		return nil, fmt.Errorf("new proxy handler: max body bytes must be positive")
	}
	return &Handler{
		router: deps.Router, registry: deps.Registry, catalog: deps.Catalog,
		credentials: deps.Credentials, client: deps.Client, observer: deps.Observer,
		maxBodyBytes: deps.MaxBodyBytes,
	}, nil
}

// Handle returns the model request handler for one downstream wire protocol.
func (h *Handler) Handle(format protocol.Format) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := h.readRequestBody(c)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		var metadata struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.Unmarshal(body, &metadata); err != nil {
			h.writeError(c, format, &protocol.ProxyError{
				Kind: protocol.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
				Code: "invalid_request", Message: "invalid request", Cause: err,
			})
			return
		}
		routed, err := h.router.Resolve(format, metadata.Model)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		credential, err := h.credentials.Resolve(c.Request.Context(), routed.ProviderID)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		profile, targetFormat, err := h.catalog.ResolveProfile(routed.ProviderID, credential.Kind, routed.ForcedTarget)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		pair, ok := h.registry.Lookup(format, targetFormat)
		if !ok {
			h.writeError(c, format, &protocol.ProxyError{
				Kind: protocol.ERROR_UNSUPPORTED_FEATURE, Status: http.StatusUnprocessableEntity,
				Code: "transform_unavailable", Message: "requested protocol transform is unavailable",
			})
			return
		}

		requestID := requestID(c.GetHeader("x-request-id"))
		startedAt := time.Now()
		defer func() {
			slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "proxy request completed",
				slog.String("request_id", requestID),
				slog.String("model", routed.Model),
				slog.String("provider", profile.ID),
				slog.String("source_format", string(format)),
				slog.String("target_format", string(targetFormat)),
				slog.Bool("stream", metadata.Stream),
				slog.Int("status", c.Writer.Status()),
				slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			)
		}()
		headers := c.Request.Header.Clone()
		headers.Set("x-request-id", requestID)
		original := protocol.RequestEnvelope{
			SourceFormat: format, TargetFormat: targetFormat, Model: metadata.Model,
			Stream: metadata.Stream, Headers: headers, Body: body,
		}
		transformInput := original
		transformInput.Model = routed.Model
		transformed, err := pair.Request(c.Request.Context(), transformInput)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		h.recordDiagnostics(c.Request.Context(), profile.ID, format, targetFormat, transformed)
		translated := protocol.RequestEnvelope{
			SourceFormat: format, TargetFormat: targetFormat, Model: routed.Model,
			Stream: metadata.Stream, Headers: headers, Body: transformed.Body,
		}
		normalized, err := profile.NormalizeRequest(translated)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		translated.Body = normalized.Body
		translated.Stream = normalized.UpstreamStream
		exchange := protocol.Exchange{
			OriginalRequest: original, TranslatedRequest: translated,
			ProviderID: profile.ID, NewID: uuid.NewString,
		}
		response, err := h.client.Do(c.Request.Context(), profile, credential, translated)
		if err != nil {
			h.writeError(c, format, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			h.handleUpstreamError(c, format, response)
			return
		}
		if normalized.BridgeToNonStream {
			h.handleBridge(c, format, targetFormat, profile, pair, exchange, response)
			return
		}
		if metadata.Stream {
			h.handleStream(c, format, targetFormat, profile, pair, exchange, response)
			return
		}
		h.handleNonStream(c, format, targetFormat, profile, pair, exchange, response)
	}
}

// HandleModels returns the deterministic model catalog exposed by provider profiles.
func (h *Handler) HandleModels() gin.HandlerFunc {
	type model struct {
		ID     string `json:"id"`
		Object string `json:"object"`
	}
	return func(c *gin.Context) {
		models := h.catalog.AdvertisedModels()
		data := make([]model, 0, len(models))
		for _, id := range models {
			data = append(data, model{ID: id, Object: "model"})
		}
		c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
	}
}

// HandleCountTokens proxies an Anthropic count request to a native provider capability.
func (h *Handler) HandleCountTokens() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := h.readRequestBody(c)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		request, model, err := decodeCountTokensRequest(body)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, &protocol.ProxyError{
				Kind: protocol.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
				Code: "invalid_request", Message: "invalid request", Cause: err,
			})
			return
		}
		routed, err := h.router.Resolve(protocol.FORMAT_ANTHROPIC_MESSAGES, model)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		credential, err := h.credentials.Resolve(c.Request.Context(), routed.ProviderID)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		profile, _, err := h.catalog.ResolveProfile(routed.ProviderID, credential.Kind, routed.ForcedTarget)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		if strings.TrimSpace(profile.CountTokensEndpoint) == "" {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, &protocol.ProxyError{
				Kind: protocol.ERROR_UNSUPPORTED_FEATURE, Status: http.StatusNotImplemented,
				Code: "unsupported_feature", Message: "native token counting is not supported",
			})
			return
		}
		request["model"], err = json.Marshal(routed.Model)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, protocolStreamError("cannot encode token count model", err))
			return
		}
		translatedBody, err := json.Marshal(request)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, protocolStreamError("cannot encode token count request", err))
			return
		}
		headers := c.Request.Header.Clone()
		headers.Set("x-request-id", requestID(c.GetHeader("x-request-id")))
		response, err := h.client.CountTokens(c.Request.Context(), profile, credential, protocol.RequestEnvelope{
			SourceFormat: protocol.FORMAT_ANTHROPIC_MESSAGES,
			TargetFormat: protocol.FORMAT_ANTHROPIC_MESSAGES,
			Model:        routed.Model, Headers: headers, Body: translatedBody,
		})
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			h.handleUpstreamError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, response)
			return
		}
		responseBody, err := readBounded(response.Body, h.maxBodyBytes)
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, err)
			return
		}
		var result struct {
			InputTokens *int `json:"input_tokens"`
		}
		if err := json.Unmarshal(responseBody, &result); err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, protocolStreamError("invalid token count response", err))
			return
		}
		if result.InputTokens == nil || *result.InputTokens < 0 {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, protocolStreamError("invalid token count response", nil))
			return
		}
		canonicalBody, err := json.Marshal(struct {
			InputTokens int `json:"input_tokens"`
		}{InputTokens: *result.InputTokens})
		if err != nil {
			h.writeError(c, protocol.FORMAT_ANTHROPIC_MESSAGES, protocolStreamError("cannot encode token count response", err))
			return
		}
		copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
		c.Header("Content-Type", "application/json")
		c.Header("x-request-id", headers.Get("x-request-id"))
		c.Data(http.StatusOK, "application/json", canonicalBody)
	}
}

func decodeCountTokensRequest(body []byte) (map[string]json.RawMessage, string, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, "", fmt.Errorf("decode token count request: %w", err)
	}
	if request == nil {
		return nil, "", fmt.Errorf("decode token count request: JSON object is required")
	}
	var model string
	if err := json.Unmarshal(request["model"], &model); err != nil {
		return nil, "", fmt.Errorf("decode token count request: model must be a string: %w", err)
	}
	if strings.TrimSpace(model) == "" {
		return nil, "", fmt.Errorf("decode token count request: model must not be blank")
	}
	return request, model, nil
}

func (h *Handler) handleStream(
	c *gin.Context,
	source, _ protocol.Format,
	profile upstream.Profile,
	pair transform.Pair,
	exchange protocol.Exchange,
	response *http.Response,
) {
	if !isEventStream(response.Header.Get("Content-Type")) {
		h.writeError(c, source, protocolStreamError("upstream did not return an event stream", nil))
		return
	}
	stream, err := pair.NewStream(exchange)
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("x-request-id", exchange.TranslatedRequest.Headers.Get("x-request-id"))
	c.Status(http.StatusOK)

	decoder := protocol.NewBoundedSSEDecoder(response.Body, h.maxBodyBytes)
	for {
		if c.Request.Context().Err() != nil {
			return
		}
		frame, decodeErr := decoder.Next()
		if errors.Is(decodeErr, io.EOF) {
			break
		}
		if decodeErr != nil {
			h.writeTerminalStreamError(c, source)
			return
		}
		frames, pushErr := stream.Push(c.Request.Context(), frame)
		if pushErr != nil {
			if c.Request.Context().Err() == nil {
				h.writeTerminalStreamError(c, source)
			}
			return
		}
		if !writeStreamFrames(c, frames) {
			return
		}
	}
	frames, err := stream.Close(c.Request.Context())
	if err != nil {
		if c.Request.Context().Err() == nil {
			h.writeTerminalStreamError(c, source)
		}
		return
	}
	_ = writeStreamFrames(c, frames)
}

func writeStreamFrames(c *gin.Context, frames []protocol.SSEFrame) bool {
	for _, frame := range frames {
		if err := protocol.WriteSSE(c.Writer, frame); err != nil {
			return false
		}
		c.Writer.Flush()
	}
	return true
}

func (h *Handler) writeTerminalStreamError(c *gin.Context, format protocol.Format) {
	var frames []protocol.SSEFrame
	switch format {
	case protocol.FORMAT_ANTHROPIC_MESSAGES:
		frames = []protocol.SSEFrame{{
			Event: "error",
			Data:  []byte(`{"type":"error","error":{"type":"api_error","message":"stream terminated"}}`),
		}}
	case protocol.FORMAT_OPENAI_CHAT:
		frames = []protocol.SSEFrame{
			{Data: []byte(`{"error":{"type":"api_error","code":"stream_error","message":"stream terminated"}}`)},
			{Data: []byte("[DONE]")},
		}
	case protocol.FORMAT_OPENAI_RESPONSES:
		frames = []protocol.SSEFrame{{
			Event: "response.failed",
			Data:  []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"stream_error","message":"stream terminated"}}}`),
		}}
	}
	_ = writeStreamFrames(c, frames)
}

func (h *Handler) handleNonStream(
	c *gin.Context,
	source, target protocol.Format,
	profile upstream.Profile,
	pair transform.Pair,
	exchange protocol.Exchange,
	response *http.Response,
) {
	body, err := readBounded(response.Body, h.maxBodyBytes)
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	result, err := pair.Response(c.Request.Context(), protocol.ResponseEnvelope{
		Status: response.StatusCode, Headers: response.Header.Clone(), Body: body, Exchange: exchange,
	})
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	h.recordDiagnostics(c.Request.Context(), profile.ID, source, target, result)
	copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
	c.Header("Content-Type", "application/json")
	c.Header("x-request-id", exchange.TranslatedRequest.Headers.Get("x-request-id"))
	c.Data(http.StatusOK, "application/json", result.Body)
}

func (h *Handler) handleBridge(
	c *gin.Context,
	source, target protocol.Format,
	profile upstream.Profile,
	pair transform.Pair,
	exchange protocol.Exchange,
	response *http.Response,
) {
	if !isEventStream(response.Header.Get("Content-Type")) {
		h.writeError(c, source, protocolStreamError("upstream did not return an event stream", nil))
		return
	}
	stream, err := pair.NewStream(exchange)
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	collector, err := transform.NewStreamCollector(source, exchange)
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	boundedCollector := newBoundedStreamCollector(collector, h.maxBodyBytes)
	upstreamBody := &io.LimitedReader{R: response.Body, N: h.maxBodyBytes + 1}
	decoder := protocol.NewBoundedSSEDecoder(upstreamBody, h.maxBodyBytes)
	for {
		frame, decodeErr := decoder.Next()
		if errors.Is(decodeErr, io.EOF) {
			if upstreamBody.N == 0 {
				h.writeError(c, source, protocolStreamError("upstream event stream exceeds limit", errUpstreamResponseTooLarge))
				return
			}
			break
		}
		if decodeErr != nil {
			h.writeError(c, source, protocolStreamError("cannot decode upstream event stream", decodeErr))
			return
		}
		frames, pushErr := stream.Push(c.Request.Context(), frame)
		if pushErr != nil {
			h.writeError(c, source, pushErr)
			return
		}
		for _, translatedFrame := range frames {
			if err := boundedCollector.Push(c.Request.Context(), translatedFrame); err != nil {
				h.writeError(c, source, err)
				return
			}
		}
	}
	closing, err := stream.Close(c.Request.Context())
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	for _, frame := range closing {
		if err := boundedCollector.Push(c.Request.Context(), frame); err != nil {
			h.writeError(c, source, err)
			return
		}
	}
	result, err := boundedCollector.Close(c.Request.Context())
	if err != nil {
		h.writeError(c, source, err)
		return
	}
	h.recordDiagnostics(c.Request.Context(), profile.ID, source, target, result)
	copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
	c.Header("Content-Type", "application/json")
	c.Header("x-request-id", exchange.TranslatedRequest.Headers.Get("x-request-id"))
	c.Data(http.StatusOK, "application/json", result.Body)
}

type boundedStreamCollector struct {
	collector transform.StreamCollector
	limit     int64
	used      int64
}

func newBoundedStreamCollector(collector transform.StreamCollector, limit int64) *boundedStreamCollector {
	return &boundedStreamCollector{collector: collector, limit: limit}
}

func (c *boundedStreamCollector) Push(ctx context.Context, frame protocol.SSEFrame) error {
	size := int64(len(frame.Event) + len(frame.ID) + len(frame.Data))
	for _, comment := range frame.Comments {
		size += int64(len(comment))
	}
	if size > c.limit-c.used {
		return protocolStreamError("collected stream exceeds limit", errUpstreamResponseTooLarge)
	}
	c.used += size
	return c.collector.Push(ctx, frame)
}

func (c *boundedStreamCollector) Close(ctx context.Context) (protocol.TransformResult, error) {
	result, err := c.collector.Close(ctx)
	if err != nil {
		return protocol.TransformResult{}, err
	}
	if int64(len(result.Body)) > c.limit {
		return protocol.TransformResult{}, protocolStreamError("collected response exceeds limit", errUpstreamResponseTooLarge)
	}
	return result, nil
}

func (h *Handler) handleUpstreamError(c *gin.Context, source protocol.Format, response *http.Response) {
	body, err := readBounded(response.Body, MAX_UPSTREAM_ERROR_BYTES)
	if err != nil {
		if errors.Is(err, errUpstreamResponseTooLarge) {
			h.writeError(c, source, transform.DecodeUpstreamError(response.StatusCode, response.Header, nil))
			return
		}
		h.writeError(c, source, err)
		return
	}
	h.writeError(c, source, transform.DecodeUpstreamError(response.StatusCode, response.Header, body))
}

func (h *Handler) recordDiagnostics(ctx context.Context, provider string, source, target protocol.Format, result protocol.TransformResult) {
	for _, warning := range result.Warnings {
		h.observer.RecordWarning(ctx, provider, source, target, warning)
	}
	for _, loss := range result.Losses {
		h.observer.RecordLoss(ctx, provider, source, target, loss)
	}
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, protocolStreamError("cannot read upstream response", err)
	}
	if int64(len(body)) > limit {
		return nil, protocolStreamError("upstream response exceeds limit", errUpstreamResponseTooLarge)
	}
	return body, nil
}

func protocolStreamError(message string, cause error) *protocol.ProxyError {
	return &protocol.ProxyError{
		Kind: protocol.ERROR_PROTOCOL, Status: http.StatusBadGateway,
		Code: "protocol_error", Message: message, Cause: cause,
	}
}

func requestID(incoming string) string {
	incoming = strings.TrimSpace(incoming)
	if incoming != "" && len(incoming) <= 128 && !containsASCIIControl(incoming) {
		return incoming
	}
	return uuid.NewString()
}

func containsASCIIControl(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return true
		}
	}
	return false
}

func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, "text/event-stream")
}

func copySafeResponseHeaders(target, source http.Header, profile upstream.Profile) {
	for name, values := range source {
		if !profile.AllowsResponseHeader(name) {
			continue
		}
		for _, value := range values {
			target.Add(name, value)
		}
	}
}

func (h *Handler) readRequestBody(c *gin.Context) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err == nil {
		return body, nil
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, &protocol.ProxyError{
			Kind: protocol.ERROR_INVALID_REQUEST, Status: http.StatusRequestEntityTooLarge,
			Code: "request_too_large", Message: "request body exceeds limit", Cause: err,
		}
	}
	return nil, &protocol.ProxyError{
		Kind: protocol.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
		Code: "invalid_request", Message: "cannot read request body", Cause: err,
	}
}

func (h *Handler) writeError(c *gin.Context, format protocol.Format, err error) {
	proxyErr := asProxyError(err)
	body, encodeErr := protocol.EncodeError(format, proxyErr)
	if encodeErr != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	if proxyErr.RetryAfter > 0 {
		c.Header("Retry-After", fmt.Sprintf("%d", int(proxyErr.RetryAfter.Seconds())))
	}
	if proxyErr.UpstreamRequestID != "" {
		c.Header("x-request-id", proxyErr.UpstreamRequestID)
	}
	c.Data(proxyErr.StatusCode(), "application/json", body)
}

func asProxyError(err error) *protocol.ProxyError {
	var proxyErr *protocol.ProxyError
	if errors.As(err, &proxyErr) {
		return proxyErr
	}
	return &protocol.ProxyError{
		Kind: protocol.ERROR_UPSTREAM, Status: http.StatusBadGateway,
		Code: "proxy_error", Message: "proxy request failed", Cause: err,
	}
}
