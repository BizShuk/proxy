package mcpimage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyClientGenerateForcesBase64AndUsesBearerAuth(t *testing.T) {
	imageBytes := testPNGBytes()
	type capturedRequest struct {
		authorization string
		path          string
		body          map[string]any
	}
	captured := make(chan capturedRequest, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(body, &payload))
		captured <- capturedRequest{
			authorization: r.Header.Get("Authorization"),
			path:          r.URL.Path,
			body:          payload,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+
			base64.StdEncoding.EncodeToString(imageBytes)+
			`","revised_prompt":"a revised prompt"}]}`)
	}))
	defer proxy.Close()

	cfg := Config{
		BaseURL:   proxy.URL,
		Port:      proxy.Listener.Addr().(*net.TCPAddr).Port,
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: DEFAULT_OUTPUT_DIR,
	}
	client, err := NewProxyClient(cfg, proxy.Client())
	require.NoError(t, err)

	images, err := client.Generate(context.Background(), GenerateRequest{
		Model:       "grok-imagine-image-quality",
		Prompt:      "a cat in space",
		N:           1,
		AspectRatio: "16:9",
		Resolution:  "1k",
	})

	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, imageBytes, images[0].Data)
	assert.Equal(t, "image/png", images[0].MIMEType)
	assert.Equal(t, "a revised prompt", images[0].RevisedPrompt)

	request := <-captured
	assert.Equal(t, "Bearer proxy-secret", request.authorization)
	assert.Equal(t, "/v1/images/generations", request.path)
	assert.Equal(t, "grok-imagine-image-quality", request.body["model"])
	assert.Equal(t, "a cat in space", request.body["prompt"])
	assert.Equal(t, "b64_json", request.body["response_format"])
	assert.Equal(t, float64(1), request.body["n"])
	assert.Equal(t, "16:9", request.body["aspect_ratio"])
	assert.Equal(t, "1k", request.body["resolution"])
}

func TestProxyClientGenerateReturnsProxyErrorBody(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid API key"}}`)
	}))
	defer proxy.Close()

	cfg := Config{
		BaseURL:   proxy.URL,
		Port:      proxy.Listener.Addr().(*net.TCPAddr).Port,
		APIKey:    "bad-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: DEFAULT_OUTPUT_DIR,
	}
	client, err := NewProxyClient(cfg, proxy.Client())
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), GenerateRequest{
		Model:  DEFAULT_MODEL,
		Prompt: "a cat in space",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 401")
	assert.Contains(t, err.Error(), "invalid API key")
	assert.NotContains(t, err.Error(), "bad-secret")
}

func TestProxyClientGenerateRejectsMissingBase64Data(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"url":"https://example.test/image.png"}]}`)
	}))
	defer proxy.Close()

	cfg := Config{
		BaseURL:   proxy.URL,
		Port:      proxy.Listener.Addr().(*net.TCPAddr).Port,
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: DEFAULT_OUTPUT_DIR,
	}
	client, err := NewProxyClient(cfg, proxy.Client())
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), GenerateRequest{
		Model:  DEFAULT_MODEL,
		Prompt: "a cat in space",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "b64_json")
}

func TestProxyClientGenerateRejectsNonImageData(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"b64_json":"`+
			base64.StdEncoding.EncodeToString([]byte("not an image"))+
			`","mime_type":"image/png"}]}`)
	}))
	defer proxy.Close()

	cfg := Config{
		BaseURL:   proxy.URL,
		Port:      proxy.Listener.Addr().(*net.TCPAddr).Port,
		APIKey:    "proxy-secret",
		Model:     DEFAULT_MODEL,
		OutputDir: DEFAULT_OUTPUT_DIR,
	}
	client, err := NewProxyClient(cfg, proxy.Client())
	require.NoError(t, err)

	_, err = client.Generate(context.Background(), GenerateRequest{
		Model:  DEFAULT_MODEL,
		Prompt: "a cat in space",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an image")
}

func testPNGBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	}
}
