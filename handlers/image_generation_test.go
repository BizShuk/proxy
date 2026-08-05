package handlers

import (
	"bytes"
	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/gosdk/file"
	"github.com/bizshuk/proxy/svc/transform"
	"github.com/bizshuk/proxy/svc/upstream"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleImageGenerationsOAuthRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type recordedRequest struct {
		path          string
		authorization string
		headers       http.Header
		body          []byte
	}
	recorded := make(chan recordedRequest, 1)
	imageAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		recorded <- recordedRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			headers:       r.Header.Clone(),
			body:          body,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "upstream-image-request")
		_, _ = io.WriteString(w, `{"created":1720000000,"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer imageAPI.Close()

	credential := &authmodel.Credential{
		Provider: "xai", Kind: authmodel.KIND_OAUTH,
		AccessToken: "xai-oauth-token", RefreshToken: "xai-refresh-token",
		ExpiresAt: time.Now().Add(time.Hour), BaseURL: sdkgrok.OAuthBaseURL,
	}
	handler := newImageHandlerForCredential(t, credential, imageAPI.Client(), imageAPI.URL)
	router := gin.New()
	router.POST("/v1/images/generations", handler.HandleImageGenerations())

	requestBody := []byte(`{
		"model":"grok-imagine-image-quality",
		"prompt":"a space cat",
		"n":1,
		"aspect_ratio":"auto",
		"resolution":"1k",
		"response_format":"b64_json"
	}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/images/generations",
		bytes.NewReader(requestBody),
	)
	request.Header.Set("x-request-id", "image-request-1")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Equal(t, "image-request-1", response.Header().Get("x-request-id"))
	assert.Equal(t, "upstream-image-request", response.Header().Get("request-id"))
	assert.JSONEq(t, `{"created":1720000000,"data":[{"b64_json":"aW1hZ2U="}]}`, response.Body.String())

	upstreamRequest := <-recorded
	assert.Equal(t, "/v1/images/generations", upstreamRequest.path)
	assert.Equal(t, "Bearer xai-oauth-token", upstreamRequest.authorization)
	assert.Equal(t, "image-request-1", upstreamRequest.headers.Get("x-request-id"))
	assert.Empty(t, upstreamRequest.headers.Get(sdkgrok.TokenAuthHeader))
	assert.Empty(t, upstreamRequest.headers.Get(sdkgrok.AuthenticateResponseHeader))
	assert.Empty(t, upstreamRequest.headers.Get("x-grok-model-override"))
	assert.JSONEq(t, string(requestBody), string(upstreamRequest.body))
}

func TestHandleImageGenerationsOpenAIAPIKeyRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	var gotAuthorization string
	var gotHeaders http.Header
	imageAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"created":1720000000,"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer imageAPI.Close()

	credential := &authmodel.Credential{
		Provider: "openai", Kind: authmodel.KIND_API_KEY, APIKey: "openai-api-key",
	}
	handler := newImageHandlerForCredential(t, credential, imageAPI.Client(), imageAPI.URL)
	router := gin.New()
	router.POST("/v1/images/generations", handler.HandleImageGenerations())

	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/images/generations",
		bytes.NewBufferString(`{"model":"gpt-image-2","prompt":"a space cat","response_format":"b64_json"}`),
	)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "/v1/images/generations", gotPath)
	assert.Equal(t, "Bearer openai-api-key", gotAuthorization)
	assert.Empty(t, gotHeaders.Get("x-grok-client-version"))
	assert.Empty(t, gotHeaders.Get("x-grok-client-identifier"))
	assert.JSONEq(t, `{"created":1720000000,"data":[{"b64_json":"aW1hZ2U="}]}`, response.Body.String())
}

func TestHandleImageGenerationsRejectsInvalidRequestBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var upstreamCalls atomic.Int32
	httpClient := &http.Client{Transport: handlerRoundTripFunc(func(*http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[]}`)),
		}, nil
	})}
	handler, err := NewHandler(newHandlerDeps(t, httpClient))
	require.NoError(t, err)
	router := gin.New()
	router.POST("/v1/images/generations", handler.HandleImageGenerations())

	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{"model":`},
		{name: "JSON object required", body: `null`},
		{name: "blank model", body: `{"model":" ","prompt":"a space cat"}`},
		{name: "blank prompt", body: `{"model":"grok-imagine-image-quality","prompt":" "}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/images/generations",
				bytes.NewBufferString(tc.body),
			)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusBadRequest, response.Code)
			assert.Contains(t, response.Body.String(), `"type":"invalid_request_error"`)
		})
	}
	assert.Zero(t, upstreamCalls.Load())
}

func newImageHandlerForCredential(
	t *testing.T,
	credential *authmodel.Credential,
	httpClient *http.Client,
	imageBaseURL string,
) *Handler {
	t.Helper()
	defaultCatalog, err := upstream.DefaultCatalog()
	require.NoError(t, err)
	modelRouter, err := defaultCatalog.NewRouter()
	require.NoError(t, err)
	registry, err := transform.NewDefaultRegistry()
	require.NoError(t, err)

	imageProfiles := make([]upstream.Profile, 0, 3)
	for _, profileID := range []string{"openai-api", "xai", upstream.XAI_GROK_OAUTH_PROFILE_ID} {
		profile, ok := defaultCatalog.Lookup(profileID)
		require.True(t, ok)
		profile.ImageGenerationBaseURL = imageBaseURL
		profile.ImageEditBaseURL = imageBaseURL
		imageProfiles = append(imageProfiles, profile)
	}
	imageCatalog, err := upstream.NewCatalog(imageProfiles)
	require.NoError(t, err)

	store, err := file.NewStore[*authmodel.Credential](t.TempDir())
	require.NoError(t, err)
	require.NoError(t, store.Write(credential.Name(), credential))
	credentials := upstream.NewCredentialResolver(
		store,
		nil,
		func(string) (string, bool) { return "", false },
		nil,
	)
	client, err := upstream.NewClient(httpClient, upstream.TimeoutConfig{
		MessagesMs: 1000, StreamMessagesMs: 1000, CountTokensMs: 1000,
	})
	require.NoError(t, err)
	handler, err := NewHandler(HandlerDeps{
		Router: modelRouter, Registry: registry, Catalog: imageCatalog,
		Credentials: credentials, Client: client,
		Observer: &recordingObserver{}, MaxBodyBytes: 1 << 20,
	})
	require.NoError(t, err)
	return handler
}
