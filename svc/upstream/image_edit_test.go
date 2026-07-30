package upstream

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientEditImagePreservesMultipartContractAndCredential(t *testing.T) {
	var gotContentType string
	var gotAuthorization string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotAuthorization = r.Header.Get("Authorization")
		var err error
		gotBody, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}]}`)
	}))
	defer server.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "gpt-image-2"))
	require.NoError(t, writer.WriteField("prompt", "Change only the outfit."))
	imagePart, err := writer.CreateFormFile("image", "portrait.png")
	require.NoError(t, err)
	_, err = imagePart.Write([]byte("image-bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	profile := defaultProfile(t, "openai-api")
	profile.ImageEditBaseURL = server.URL
	client, err := NewClient(server.Client(), timeoutConfig())
	require.NoError(t, err)
	response, err := client.EditImage(context.Background(), profile, &authmodel.Credential{
		Provider: "openai", Kind: authmodel.KIND_API_KEY, APIKey: "openai-api-key",
	}, model.RequestEnvelope{
		Model:       "gpt-image-2",
		ContentType: writer.FormDataContentType(),
		Headers:     http.Header{"x-request-id": {"image-edit-request-1"}},
		Body:        body.Bytes(),
	})
	require.NoError(t, err)
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[{"b64_json":"aW1hZ2U="}]}`, string(responseBody))
	assert.Equal(t, writer.FormDataContentType(), gotContentType)
	assert.Equal(t, "Bearer openai-api-key", gotAuthorization)
	assert.Equal(t, body.Bytes(), gotBody)
}

func TestClientEditImageRejectsProfileWithoutCapability(t *testing.T) {
	profile := defaultProfile(t, "openai-api")
	profile.ImageEditBaseURL = ""
	profile.ImageEditEndpoint = ""
	client, err := NewClient(http.DefaultClient, timeoutConfig())
	require.NoError(t, err)

	response, err := client.EditImage(context.Background(), profile, &authmodel.Credential{
		Provider: "openai", Kind: authmodel.KIND_API_KEY, APIKey: "openai-api-key",
	}, model.RequestEnvelope{Model: "gpt-image-2", Body: []byte("multipart")})

	require.Error(t, err)
	assert.Nil(t, response)
	var proxyErr *model.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, model.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}
