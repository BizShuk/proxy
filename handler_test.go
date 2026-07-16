package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/auth"
	"github.com/bizshuk/agentsdk/config"
	"github.com/bizshuk/proxy/protocol"
	"github.com/bizshuk/proxy/protocol/anthropic"
	"github.com/bizshuk/proxy/protocol/chat"
	"github.com/bizshuk/proxy/protocol/responses"
	"github.com/bizshuk/proxy/transform"
	"github.com/bizshuk/proxy/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingObserver struct {
	warnings []protocol.Warning
	losses   []protocol.SemanticLoss
}

type handlerProviderCase struct {
	name           string
	credential     *auth.Credential
	qualifiedModel string
	wantPath       string
	wantModel      string
}

type handlerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn handlerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type fixedStreamCollector struct {
	result protocol.TransformResult
}

func (*fixedStreamCollector) Push(context.Context, protocol.SSEFrame) error { return nil }

func (c *fixedStreamCollector) Close(context.Context) (protocol.TransformResult, error) {
	return c.result, nil
}

var handlerSourceCases = []struct {
	name   string
	format protocol.Format
}{
	{name: "anthropic", format: protocol.FORMAT_ANTHROPIC_MESSAGES},
	{name: "chat", format: protocol.FORMAT_OPENAI_CHAT},
	{name: "responses", format: protocol.FORMAT_OPENAI_RESPONSES},
}

func TestHandlerRoutesAllProviderAndSourceCombinationsNonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type upstreamRequest struct {
		path          string
		authorization string
		apiKey        string
		body          []byte
	}
	recorded := make(chan upstreamRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		recorded <- upstreamRequest{
			path: r.URL.Path, authorization: r.Header.Get("Authorization"),
			apiKey: r.Header.Get("x-api-key"), body: body,
		}
		if r.Header.Get("Accept") == "text/event-stream" {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, successSSEForPath(r.URL.Path))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(successBodyForPath(r.URL.Path))
	}))
	defer server.Close()

	for _, providerCase := range handlerProviderCases(server.URL) {
		for _, sourceCase := range handlerSourceCases {
			t.Run(providerCase.name+"/"+sourceCase.name, func(t *testing.T) {
				handler := newHandlerForCredential(t, providerCase.credential, server.Client())
				router := gin.New()
				router.POST("/model", handler.Handle(sourceCase.format))
				request := httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(sourceCase.format, providerCase.qualifiedModel, false)))
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
				assertResponseDecodes(t, sourceCase.format, response.Body.Bytes())
				upstreamCall := <-recorded
				assert.Equal(t, providerCase.wantPath, upstreamCall.path)
				assert.Equal(t, providerCase.wantModel, bodyModel(t, upstreamCall.body))
				if providerCase.name == "openai codex oauth" {
					assert.True(t, bodyStream(t, upstreamCall.body))
				}
				if providerCase.credential.Kind == auth.KIND_OAUTH || providerCase.credential.Provider == "openai" || providerCase.credential.Provider == "xai" {
					assert.NotEmpty(t, upstreamCall.authorization)
				} else {
					assert.NotEmpty(t, upstreamCall.apiKey)
				}
			})
		}
	}
}

func TestHandlerRoutesAllProviderAndSourceCombinationsStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type upstreamRequest struct {
		path string
		body []byte
	}
	recorded := make(chan upstreamRequest, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		recorded <- upstreamRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = io.WriteString(w, successSSEForPath(r.URL.Path))
	}))
	defer server.Close()

	for _, providerCase := range handlerProviderCases(server.URL) {
		for _, sourceCase := range handlerSourceCases {
			t.Run(providerCase.name+"/"+sourceCase.name, func(t *testing.T) {
				handler := newHandlerForCredential(t, providerCase.credential, server.Client())
				router := gin.New()
				router.POST("/model", handler.Handle(sourceCase.format))
				request := httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(sourceCase.format, providerCase.qualifiedModel, true)))
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)

				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				assert.True(t, strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream"))
				assertTerminalSSE(t, sourceCase.format, response.Body.Bytes())
				upstreamCall := <-recorded
				assert.Equal(t, providerCase.wantPath, upstreamCall.path)
				assert.Equal(t, providerCase.wantModel, bodyModel(t, upstreamCall.body))
				assert.True(t, bodyStream(t, upstreamCall.body))
			})
		}
	}
}

func handlerProviderCases(baseURL string) []handlerProviderCase {
	return []handlerProviderCase{
		{name: "anthropic", credential: apiKeyCred("anthropic", baseURL), qualifiedModel: "anthropic/claude-3-5-sonnet-latest", wantPath: "/v1/messages", wantModel: "claude-3-5-sonnet-latest"},
		{name: "minimax", credential: apiKeyCred("minimax", baseURL), qualifiedModel: "minimax/minimax-m3", wantPath: "/v1/messages", wantModel: "minimax-m3"},
		{name: "openai api", credential: apiKeyCred("openai", baseURL), qualifiedModel: "openai/gpt-5", wantPath: "/v1/responses", wantModel: "gpt-5"},
		{name: "openai codex oauth", credential: oauthCred("openai", baseURL), qualifiedModel: "openai/gpt-5", wantPath: "/codex/responses", wantModel: "gpt-5"},
		{name: "xai", credential: apiKeyCred("xai", baseURL), qualifiedModel: "xai/grok-4.5", wantPath: "/v1/responses", wantModel: "grok-4.5"},
		{name: "xai forced chat", credential: apiKeyCred("xai", baseURL), qualifiedModel: "xai-chat/grok-4.5", wantPath: "/v1/chat/completions", wantModel: "grok-4.5"},
		{name: "openai forced chat", credential: apiKeyCred("openai", baseURL), qualifiedModel: "openai-chat/gpt-5", wantPath: "/v1/chat/completions", wantModel: "gpt-5"},
	}
}

func apiKeyCred(provider, baseURL string) *auth.Credential {
	return &auth.Credential{Provider: provider, Kind: auth.KIND_API_KEY, APIKey: "test-api-key", BaseURL: baseURL}
}

func oauthCred(provider, baseURL string) *auth.Credential {
	return &auth.Credential{
		Provider: provider, Kind: auth.KIND_OAUTH, AccessToken: "test-access-token",
		RefreshToken: "test-refresh-token", ExpiresAt: time.Now().Add(time.Hour), BaseURL: baseURL,
	}
}

func requestBody(format protocol.Format, model string, stream bool) []byte {
	var body string
	switch format {
	case protocol.FORMAT_ANTHROPIC_MESSAGES:
		body = fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%t}`, model, stream)
	case protocol.FORMAT_OPENAI_CHAT:
		body = fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hello"}],"stream":%t}`, model, stream)
	case protocol.FORMAT_OPENAI_RESPONSES:
		body = fmt.Sprintf(`{"model":%q,"input":"hello","stream":%t}`, model, stream)
	}
	return []byte(body)
}

func successBodyForPath(path string) []byte {
	switch path {
	case "/v1/messages":
		return []byte(`{"id":"msg_up","type":"message","role":"assistant","content":[{"type":"text","text":"hello"}],"model":"upstream","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	case "/v1/chat/completions":
		return []byte(`{"id":"chat_up","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	default:
		return []byte(`{"id":"resp_up","object":"response","model":"upstream","status":"completed","output":[{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}
}

func successSSEForPath(path string) string {
	switch path {
	case "/v1/messages":
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_up\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"upstream\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	case "/v1/chat/completions":
		return "data: {\"id\":\"chat_up\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n" +
			"data: [DONE]\n\n"
	default:
		return "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_up\",\"object\":\"response\",\"model\":\"upstream\",\"status\":\"in_progress\"}}\n\n" +
			"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"item_1\",\"delta\":\"hello\"}\n\n" +
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_up\",\"object\":\"response\",\"model\":\"upstream\",\"status\":\"completed\",\"output\":[{\"id\":\"item_1\",\"type\":\"message\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"
	}
}

func assertResponseDecodes(t *testing.T, format protocol.Format, body []byte) {
	t.Helper()
	var err error
	switch format {
	case protocol.FORMAT_ANTHROPIC_MESSAGES:
		_, err = anthropic.DecodeResponse(body)
	case protocol.FORMAT_OPENAI_CHAT:
		_, err = chat.DecodeResponse(body)
	case protocol.FORMAT_OPENAI_RESPONSES:
		_, err = responses.DecodeResponse(body)
	}
	require.NoError(t, err, string(body))
}

func bodyModel(t *testing.T, body []byte) string {
	t.Helper()
	var value struct {
		Model string `json:"model"`
	}
	require.NoError(t, json.Unmarshal(body, &value))
	return value.Model
}

func bodyStream(t *testing.T, body []byte) bool {
	t.Helper()
	var value struct {
		Stream bool `json:"stream"`
	}
	require.NoError(t, json.Unmarshal(body, &value))
	return value.Stream
}

func assertTerminalSSE(t *testing.T, format protocol.Format, body []byte) {
	t.Helper()
	decoder := protocol.NewSSEDecoder(bytes.NewReader(body))
	var frames []protocol.SSEFrame
	for {
		frame, err := decoder.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err, string(body))
		frames = append(frames, frame)
	}
	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	switch format {
	case protocol.FORMAT_ANTHROPIC_MESSAGES:
		assert.Equal(t, "message_stop", last.Event)
	case protocol.FORMAT_OPENAI_CHAT:
		assert.Equal(t, "[DONE]", string(last.Data))
	case protocol.FORMAT_OPENAI_RESPONSES:
		assert.Equal(t, "response.completed", last.Event)
	}
}

func newHandlerForCredential(t *testing.T, credential *auth.Credential, httpClient *http.Client) *Handler {
	return newHandlerForCredentialWithLimit(t, credential, httpClient, 1<<20)
}

func newHandlerForCredentialWithLimit(t *testing.T, credential *auth.Credential, httpClient *http.Client, limit int64) *Handler {
	t.Helper()
	catalog, err := upstream.DefaultCatalog()
	require.NoError(t, err)
	router, err := catalog.NewRouter()
	require.NoError(t, err)
	registry, err := transform.NewDefaultRegistry()
	require.NoError(t, err)
	store, err := auth.NewFileStore(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.Save(credential))
	credentials := upstream.NewCredentialResolver(store, nil, func(string) (string, bool) { return "", false })
	client, err := upstream.NewClient(httpClient, config.ProxyTimeoutConfig{
		MessagesMs: 1000, StreamMessagesMs: 1000, CountTokensMs: 1000,
	})
	require.NoError(t, err)
	handler, err := NewHandler(HandlerDeps{
		Router: router, Registry: registry, Catalog: catalog, Credentials: credentials,
		Client: client, Observer: &recordingObserver{}, MaxBodyBytes: limit,
	})
	require.NoError(t, err)
	return handler
}

func (o *recordingObserver) RecordWarning(_ context.Context, _ string, _, _ protocol.Format, warning protocol.Warning) {
	o.warnings = append(o.warnings, warning)
}

func (o *recordingObserver) RecordLoss(_ context.Context, _ string, _, _ protocol.Format, loss protocol.SemanticLoss) {
	o.losses = append(o.losses, loss)
}

func TestNewHandlerValidatesDependencies(t *testing.T) {
	deps := newHandlerDeps(t, nil)
	tests := []struct {
		name   string
		mutate func(*HandlerDeps)
	}{
		{name: "nil router", mutate: func(deps *HandlerDeps) { deps.Router = nil }},
		{name: "nil registry", mutate: func(deps *HandlerDeps) { deps.Registry = nil }},
		{name: "nil catalog", mutate: func(deps *HandlerDeps) { deps.Catalog = nil }},
		{name: "nil credentials", mutate: func(deps *HandlerDeps) { deps.Credentials = nil }},
		{name: "nil client", mutate: func(deps *HandlerDeps) { deps.Client = nil }},
		{name: "nil observer", mutate: func(deps *HandlerDeps) { deps.Observer = nil }},
		{name: "zero body limit", mutate: func(deps *HandlerDeps) { deps.MaxBodyBytes = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := deps
			tc.mutate(&candidate)
			handler, err := NewHandler(candidate)
			require.Error(t, err)
			assert.Nil(t, handler)
		})
	}
}

func TestHandlerRejectsMalformedAndUnknownRequestsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := NewHandler(newHandlerDeps(t, nil))
	require.NoError(t, err)

	tests := []struct {
		name       string
		format     protocol.Format
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "malformed anthropic", format: protocol.FORMAT_ANTHROPIC_MESSAGES, body: `{`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown chat model", format: protocol.FORMAT_OPENAI_CHAT, body: `{"model":"mystery","messages":[]}`, wantStatus: http.StatusBadRequest, wantCode: "unknown_model"},
		{name: "unknown responses model", format: protocol.FORMAT_OPENAI_RESPONSES, body: `{"model":"mystery","input":"hi"}`, wantStatus: http.StatusBadRequest, wantCode: "unknown_model"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.POST("/model", handler.Handle(tc.format))
			request := httptest.NewRequest(http.MethodPost, "/model", bytes.NewBufferString(tc.body))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			assert.Equal(t, tc.wantStatus, response.Code)
			assert.Contains(t, response.Body.String(), tc.wantCode)
		})
	}
}

func TestHandlerEnforcesRequestAndUpstreamBodyLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("request body", func(t *testing.T) {
		deps := newHandlerDeps(t, nil)
		deps.MaxBodyBytes = 16
		handler, err := NewHandler(deps)
		require.NoError(t, err)
		router := gin.New()
		router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", strings.NewReader(`{"model":"gpt-5","messages":[]}`)))
		assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		assert.Contains(t, response.Body.String(), "request_too_large")
	})

	for _, tc := range []struct {
		name       string
		status     int
		wantStatus int
	}{
		{name: "successful upstream", status: http.StatusOK, wantStatus: http.StatusBadGateway},
		{name: "error upstream", status: http.StatusTooManyRequests, wantStatus: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				size := 257
				if tc.status != http.StatusOK {
					size = int(MAX_UPSTREAM_ERROR_BYTES + 1)
				}
				_, _ = w.Write(bytes.Repeat([]byte("x"), size))
			}))
			defer server.Close()
			handler := newHandlerForCredentialWithLimit(t, apiKeyCred("xai", server.URL), server.Client(), 256)
			router := gin.New()
			router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "xai/grok-4.5", false))))
			assert.Equal(t, tc.wantStatus, response.Code)
			if tc.status == http.StatusOK {
				assert.Contains(t, response.Body.String(), "protocol_error")
			}
		})
	}
}

func TestHandlerOversizedUpstreamErrorPreservesMetadataWithoutParsingPartialBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "11")
		w.Header().Set("x-request-id", "oversized-request")
		w.WriteHeader(http.StatusTooManyRequests)
		body := `{"error":{"code":"partial_code","message":"partial body must not be parsed"}}`
		_, _ = io.WriteString(w, body+strings.Repeat(" ", int(MAX_UPSTREAM_ERROR_BYTES)))
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, apiKeyCred("xai", server.URL), server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "xai/grok-4.5", false))))

	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "11", response.Header().Get("Retry-After"))
	assert.Equal(t, "oversized-request", response.Header().Get("x-request-id"))
	assert.Contains(t, response.Body.String(), "upstream request failed")
	assert.NotContains(t, response.Body.String(), "partial body must not be parsed")
	assert.NotContains(t, response.Body.String(), "partial_code")
}

func TestHandlerPreservesUpstreamRateLimitMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.Header().Set("x-request-id", "upstream-request")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"rate_limited","message":"slow down"}}`)
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, apiKeyCred("xai", server.URL), server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_ANTHROPIC_MESSAGES))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_ANTHROPIC_MESSAGES, "xai/grok-4.5", false))))

	assert.Equal(t, http.StatusTooManyRequests, response.Code)
	assert.Equal(t, "7", response.Header().Get("Retry-After"))
	assert.Equal(t, "upstream-request", response.Header().Get("x-request-id"))
	assert.Contains(t, response.Body.String(), "slow down")
}

func TestHandlerEmitsTerminalErrorWhenUpstreamStreamEndsEarly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, apiKeyCred("xai", server.URL), server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "xai/grok-4.5", true))))

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "stream terminated")
	assert.Contains(t, response.Body.String(), "[DONE]")
}

func TestHandlerEmitsTerminalErrorWhenOneUpstreamSSEFrameExceedsLimit(t *testing.T) {
	oversized := strings.Repeat("x", 512)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
		_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", oversized)
	}))
	defer server.Close()
	handler := newHandlerForCredentialWithLimit(t, apiKeyCred("xai", server.URL), server.Client(), 256)
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "xai/grok-4.5", true))))

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "stream terminated")
	assert.Contains(t, response.Body.String(), "[DONE]")
	assert.NotContains(t, response.Body.String(), oversized)
}

func TestHandlerNonStreamBridgeDoesNotCommitPartialSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n")
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, oauthCred("openai", server.URL), server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "openai/gpt-5", false))))

	assert.Equal(t, http.StatusBadGateway, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), "protocol_error")
	assert.NotContains(t, response.Body.String(), "response.created")
}

func TestHandlerNonStreamBridgeRejectsUpstreamSSETotalOverLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5\",\"status\":\"in_progress\"}}\n\n")
		for _, delta := range []string{"one", "two", "three"} {
			_, _ = fmt.Fprintf(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", delta)
		}
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()
	handler := newHandlerForCredentialWithLimit(t, oauthCred("openai", server.URL), server.Client(), 256)
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "openai/gpt-5", false))))

	assert.Equal(t, http.StatusBadGateway, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Contains(t, response.Body.String(), "protocol_error")
}

func TestBoundedStreamCollectorLimitsTranslatedFramesAndFinalResult(t *testing.T) {
	t.Run("translated frames", func(t *testing.T) {
		collector := newBoundedStreamCollector(&fixedStreamCollector{}, 8)
		err := collector.Push(context.Background(), protocol.SSEFrame{Data: []byte("123456789")})
		require.ErrorIs(t, err, errUpstreamResponseTooLarge)
	})

	t.Run("final result", func(t *testing.T) {
		collector := newBoundedStreamCollector(&fixedStreamCollector{
			result: protocol.TransformResult{Body: []byte("123456789")},
		}, 8)
		_, err := collector.Close(context.Background())
		require.ErrorIs(t, err, errUpstreamResponseTooLarge)
	})
}

func TestHandlerRejectsUnsupportedProviderCapabilityBeforeUpstream(t *testing.T) {
	contacted := make(chan struct{}, 1)
	client := &http.Client{Transport: handlerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		contacted <- struct{}{}
		return nil, errors.New("unexpected upstream call")
	})}
	handler := newHandlerForCredential(t, apiKeyCred("xai", "http://127.0.0.1"), client)
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_RESPONSES))
	response := httptest.NewRecorder()
	body := `{"model":"xai/grok-4.5","input":"hello","tools":[{"type":"web_search"}]}`
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/model", strings.NewReader(body)))

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "unsupported_tool")
	select {
	case <-contacted:
		t.Fatal("unsupported request contacted upstream")
	default:
	}
}

func TestHandlerCancelsUpstreamWithDownstreamContext(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	client := &http.Client{Transport: handlerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(canceled)
		return nil, r.Context().Err()
	})}
	handler := newHandlerForCredential(t, apiKeyCred("xai", "http://127.0.0.1"), client)
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/model", bytes.NewReader(requestBody(protocol.FORMAT_OPENAI_CHAT, "xai/grok-4.5", true))).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream context was not canceled")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after cancellation")
	}
}

func TestHandlerModelsUsesCatalog(t *testing.T) {
	handler, err := NewHandler(newHandlerDeps(t, nil))
	require.NoError(t, err)
	router := gin.New()
	router.GET("/models", handler.HandleModels())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))

	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	var models []string
	for _, item := range payload.Data {
		models = append(models, item.ID)
	}
	assert.Equal(t, []string{"MiniMax-Text-01", "claude-", "gpt-", "grok-", "minimax-", "o1-", "o3-"}, models)
}

func TestHandlerCountTokensUsesNativeAnthropicCapability(t *testing.T) {
	var gotPath string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		gotModel = bodyModel(t, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":42}`)
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, apiKeyCred("anthropic", server.URL), server.Client())
	router := gin.New()
	router.POST("/count", handler.HandleCountTokens())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/count", bytes.NewReader(requestBody(protocol.FORMAT_ANTHROPIC_MESSAGES, "anthropic/claude-3-5-sonnet-latest", false))))

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "/v1/messages/count_tokens", gotPath)
	assert.Equal(t, "claude-3-5-sonnet-latest", gotModel)
	assert.JSONEq(t, `{"input_tokens":42}`, response.Body.String())
}

func TestHandlerCountTokensPreservesUnknownRequestFieldsAndCanonicalizesResponse(t *testing.T) {
	var upstreamBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		upstreamBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		_, _ = io.WriteString(w, `{"trace":"discard","input_tokens":42}`)
	}))
	defer server.Close()
	handler := newHandlerForCredential(t, apiKeyCred("anthropic", server.URL), server.Client())
	router := gin.New()
	router.POST("/count", handler.HandleCountTokens())
	requestBody := `{"model":"anthropic/claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hello"}],"custom_extension":{"enabled":true}}`
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/count", strings.NewReader(requestBody)))

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"input_tokens":42}`, response.Body.String())
	var forwarded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(upstreamBody, &forwarded))
	assert.JSONEq(t, `{"enabled":true}`, string(forwarded["custom_extension"]))
	assert.JSONEq(t, `"claude-3-5-sonnet-latest"`, string(forwarded["model"]))
}

func TestHandlerCountTokensRequiresNonNegativeInputTokens(t *testing.T) {
	for _, body := range []string{`{}`, `{"input_tokens":-1}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			handler := newHandlerForCredential(t, apiKeyCred("anthropic", server.URL), server.Client())
			router := gin.New()
			router.POST("/count", handler.HandleCountTokens())
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/count", bytes.NewReader(requestBody(protocol.FORMAT_ANTHROPIC_MESSAGES, "anthropic/claude-3-5-sonnet-latest", false))))

			assert.Equal(t, http.StatusBadGateway, response.Code)
			assert.Contains(t, response.Body.String(), "invalid token count response")
			assert.Contains(t, response.Body.String(), `"type":"error"`)
		})
	}
}

func TestHandlerCountTokensRejectsUnsupportedProvider(t *testing.T) {
	handler := newHandlerForCredential(t, apiKeyCred("xai", "http://127.0.0.1"), http.DefaultClient)
	router := gin.New()
	router.POST("/count", handler.HandleCountTokens())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/count", bytes.NewReader(requestBody(protocol.FORMAT_ANTHROPIC_MESSAGES, "xai/grok-4.5", false))))

	assert.Equal(t, http.StatusNotImplemented, response.Code)
	assert.Contains(t, response.Body.String(), "native token counting")
}

func TestHandlerRecordsEachTransformDiagnosticOnce(t *testing.T) {
	observer := &recordingObserver{}
	deps := newHandlerDeps(t, nil)
	deps.Observer = observer
	handler, err := NewHandler(deps)
	require.NoError(t, err)
	handler.recordDiagnostics(context.Background(), "xai", protocol.FORMAT_OPENAI_CHAT, protocol.FORMAT_OPENAI_RESPONSES, protocol.TransformResult{
		Warnings: []protocol.Warning{{Code: "warning"}},
		Losses:   []protocol.SemanticLoss{{Field: "messages.name"}},
	})
	assert.Equal(t, []protocol.Warning{{Code: "warning"}}, observer.warnings)
	assert.Equal(t, []protocol.SemanticLoss{{Field: "messages.name"}}, observer.losses)
}

func TestHandlerCompletionLogIsRedacted(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(successBodyForPath("/v1/responses"))
	}))
	defer server.Close()
	credential := apiKeyCred("xai", server.URL)
	credential.APIKey = "super-secret-api-key"
	handler := newHandlerForCredential(t, credential, server.Client())
	router := gin.New()
	router.POST("/model", handler.Handle(protocol.FORMAT_OPENAI_RESPONSES))
	body := `{"model":"xai/grok-4.5","input":"private prompt and tool output","stream":false}`
	request := httptest.NewRequest(http.MethodPost, "/model", strings.NewReader(body))
	request.Header.Set("x-request-id", "request-123")
	request.Header.Set("Authorization", "Bearer downstream-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	output := logs.String()
	assert.Contains(t, output, "request-123")
	assert.Contains(t, output, "grok-4.5")
	assert.Contains(t, output, "xai")
	assert.NotContains(t, output, "private prompt")
	assert.NotContains(t, output, "tool output")
	assert.NotContains(t, output, "super-secret-api-key")
	assert.NotContains(t, output, "downstream-secret")
}

func TestRequestIDRejectsASCIIControlCharacters(t *testing.T) {
	for _, input := range []string{"bad\tid", "bad\x00id", "bad\x7fid", "bad\nid"} {
		assert.NotEqual(t, input, requestID(input))
	}
	assert.Equal(t, "valid-request", requestID(" valid-request "))
}

func newHandlerDeps(t *testing.T, httpClient *http.Client) HandlerDeps {
	t.Helper()
	catalog, err := upstream.DefaultCatalog()
	require.NoError(t, err)
	router, err := catalog.NewRouter()
	require.NoError(t, err)
	registry, err := transform.NewDefaultRegistry()
	require.NoError(t, err)
	store, err := auth.NewFileStore(t.TempDir())
	require.NoError(t, err)
	credentials := upstream.NewCredentialResolver(store, nil, func(string) (string, bool) { return "", false })
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client, err := upstream.NewClient(httpClient, config.ProxyTimeoutConfig{
		MessagesMs: 1000, StreamMessagesMs: 1000, CountTokensMs: 1000,
	})
	require.NoError(t, err)
	return HandlerDeps{
		Router: router, Registry: registry, Catalog: catalog, Credentials: credentials,
		Client: client, Observer: &recordingObserver{}, MaxBodyBytes: 1 << 20,
	}
}
