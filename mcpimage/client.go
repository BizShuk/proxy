package mcpimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	MAX_PROXY_RESPONSE_BYTES = int64(256 << 20)
	MAX_PROXY_ERROR_BYTES    = 64 << 10
)

// GenerateRequest is the supported image-generation subset sent to the proxy.
type GenerateRequest struct {
	Model       string `json:"model"`
	Prompt      string `json:"prompt"`
	N           int    `json:"n,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
}

// GeneratedImage is one decoded image returned by the proxy.
type GeneratedImage struct {
	Data          []byte
	MIMEType      string
	RevisedPrompt string
}

// ProxyClient calls the proxy image-generation endpoint.
type ProxyClient struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// NewProxyClient returns a client bound to one proxy endpoint.
func NewProxyClient(cfg Config, httpClient *http.Client) (*ProxyClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, fmt.Errorf("new image proxy client: HTTP client is required")
	}
	endpoint, err := cfg.EndpointURL()
	if err != nil {
		return nil, err
	}

	clone := *httpClient
	clone.Timeout = DEFAULT_REQUEST_TIMEOUT
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &ProxyClient{
		endpoint:   endpoint,
		apiKey:     cfg.APIKey,
		httpClient: &clone,
	}, nil
}

// Generate requests Base64 output from the proxy and decodes every image.
func (c *ProxyClient) Generate(ctx context.Context, request GenerateRequest) ([]GeneratedImage, error) {
	if c == nil || c.httpClient == nil {
		return nil, fmt.Errorf("generate image: proxy client is unavailable")
	}
	if ctx == nil {
		return nil, fmt.Errorf("generate image: context is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, fmt.Errorf("generate image: model must not be blank")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, fmt.Errorf("generate image: prompt must not be blank")
	}
	if request.N < 0 || request.N > 10 {
		return nil, fmt.Errorf("generate image: n must be between 1 and 10 when provided")
	}

	payload := struct {
		GenerateRequest
		ResponseFormat string `json:"response_format"`
	}{
		GenerateRequest: request,
		ResponseFormat:  "b64_json",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("generate image: encode proxy request: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("generate image: create proxy request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("generate image: call proxy: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := readResponseBody(response.Body, MAX_PROXY_RESPONSE_BYTES)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		errorBody := responseBody
		if len(errorBody) > MAX_PROXY_ERROR_BYTES {
			errorBody = errorBody[:MAX_PROXY_ERROR_BYTES]
		}
		return nil, fmt.Errorf(
			"generate image: proxy returned HTTP %d: %s",
			response.StatusCode,
			strings.TrimSpace(string(errorBody)),
		)
	}

	var decoded struct {
		Data []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("generate image: decode proxy response: %w", err)
	}
	if len(decoded.Data) == 0 {
		return nil, fmt.Errorf("generate image: proxy response contains no images")
	}

	images := make([]GeneratedImage, 0, len(decoded.Data))
	for index, item := range decoded.Data {
		if strings.TrimSpace(item.B64JSON) == "" {
			return nil, fmt.Errorf("generate image: proxy image %d is missing b64_json", index)
		}
		data, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("generate image: decode proxy image %d b64_json: %w", index, err)
		}
		mimeType, err := imageMIMEType(data)
		if err != nil {
			return nil, fmt.Errorf("generate image: proxy image %d: %w", index, err)
		}
		images = append(images, GeneratedImage{
			Data:          data,
			MIMEType:      mimeType,
			RevisedPrompt: item.RevisedPrompt,
		})
	}
	return images, nil
}

func readResponseBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("generate image: read proxy response: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("generate image: proxy response exceeds %d bytes", limit)
	}
	return body, nil
}

func imageMIMEType(data []byte) (string, error) {
	detected := http.DetectContentType(data)
	if strings.HasPrefix(detected, "image/") {
		return detected, nil
	}
	return "", fmt.Errorf("decoded data is not an image")
}
