package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleImageEditsForwardsMultipartRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type recordedRequest struct {
		path          string
		authorization string
		contentType   string
		body          []byte
	}
	recorded := make(chan recordedRequest, 1)
	imageAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		recorded <- recordedRequest{
			path:          r.URL.Path,
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			body:          body,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("request-id", "upstream-edit-request")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer imageAPI.Close()

	credential := &authmodel.Credential{
		Provider: "openai", Kind: authmodel.KIND_API_KEY, APIKey: "openai-api-key",
	}
	handler := newImageHandlerForCredential(t, credential, imageAPI.Client(), imageAPI.URL)
	router := gin.New()
	router.POST("/v1/images/edits", handler.HandleImageEdits())

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "Change only the selected outfit."))
	imagePart, err := writer.CreateFormFile("image", "portrait.png")
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	expectedBody := append([]byte(nil), body.Bytes()...)

	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("x-request-id", "image-edit-request-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.Equal(t, "image-edit-request-1", response.Header().Get("x-request-id"))
	assert.Equal(t, "upstream-edit-request", response.Header().Get("request-id"))
	assert.JSONEq(t, `{"data":[{"b64_json":"aW1hZ2U="}]}`, response.Body.String())

	upstreamRequest := <-recorded
	assert.Equal(t, "/v1/images/edits", upstreamRequest.path)
	assert.Equal(t, "Bearer openai-api-key", upstreamRequest.authorization)
	assert.Equal(t, writer.FormDataContentType(), upstreamRequest.contentType)
	assert.Equal(t, expectedBody, upstreamRequest.body)
}

func TestHandleImageEditsRejectsMalformedMultipartBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, err := NewHandler(newHandlerDeps(t, http.DefaultClient))
	require.NoError(t, err)
	router := gin.New()
	router.POST("/v1/images/edits", handler.HandleImageEdits())

	request := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewBufferString("not multipart"))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `"type":"invalid_request_error"`)
}
