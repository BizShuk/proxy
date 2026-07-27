package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/gin-gonic/gin"
)

const (
	XAI_PROVIDER_FAMILY    = "xai"
	OPENAI_PROVIDER_FAMILY = "openai"
)

type imageGenerationRequestMetadata struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

// HandleImageGenerations returns an OpenAI-compatible image-generation
// handler. The request and response JSON remain unchanged; this layer only
// selects the provider from the model name, resolves credentials, enforces
// bounds, and owns the upstream lifecycle.
func (h *Handler) HandleImageGenerations() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := h.readRequestBody(c)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}
		metadata, err := decodeImageGenerationRequest(body)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, invalidImageGenerationRequest(err))
			return
		}

		requestIDValue := requestID(c.GetHeader("x-request-id"))
		providerFamily := imageProviderFamily(metadata.Model)
		credential, err := h.credentials.Resolve(c.Request.Context(), providerFamily)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}
		profile, _, err := h.catalog.ResolveProfile(providerFamily, credential.Kind, nil)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}

		slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "proxy image generation routed",
			slog.String("request_id", requestIDValue),
			slog.String("model", metadata.Model),
			slog.String("routed_family", providerFamily),
			slog.String("provider", profile.ID),
			slog.String("credential_kind", string(credential.Kind)),
		)
		startedAt := time.Now()
		defer func() {
			slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "proxy image generation completed",
				slog.String("request_id", requestIDValue),
				slog.String("model", metadata.Model),
				slog.String("provider", profile.ID),
				slog.Int("status", c.Writer.Status()),
				slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			)
		}()

		headers := c.Request.Header.Clone()
		headers.Set("x-request-id", requestIDValue)
		response, err := h.client.GenerateImage(c.Request.Context(), profile, credential, model.RequestEnvelope{
			SourceFormat: model.FORMAT_OPENAI_RESPONSES,
			TargetFormat: model.FORMAT_OPENAI_RESPONSES,
			Model:        metadata.Model,
			Headers:      headers,
			Body:         body,
		})
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}
		defer response.Body.Close()

		responseBody, err := readBounded(response.Body, h.maxBodyBytes)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			response.Body = io.NopCloser(bytes.NewReader(responseBody))
			h.logUpstreamError(
				c.Request.Context(),
				requestIDValue,
				metadata.Model,
				profile.ID,
				response,
			)
		}

		copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
		c.Header("x-request-id", requestIDValue)
		contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
		if contentType == "" {
			contentType = "application/json"
		}
		c.Data(response.StatusCode, contentType, responseBody)
	}
}

func imageProviderFamily(modelName string) string {
	normalized := strings.ToLower(strings.TrimSpace(modelName))
	if strings.HasPrefix(normalized, "gpt-image-") || strings.HasPrefix(normalized, "dall-e-") {
		return OPENAI_PROVIDER_FAMILY
	}
	return XAI_PROVIDER_FAMILY
}

func decodeImageGenerationRequest(body []byte) (imageGenerationRequestMetadata, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return imageGenerationRequestMetadata{}, fmt.Errorf("decode image generation request: %w", err)
	}
	if request == nil {
		return imageGenerationRequestMetadata{}, fmt.Errorf("decode image generation request: JSON object is required")
	}

	var metadata imageGenerationRequestMetadata
	if err := json.Unmarshal(body, &metadata); err != nil {
		return imageGenerationRequestMetadata{}, fmt.Errorf("decode image generation metadata: %w", err)
	}
	if strings.TrimSpace(metadata.Model) == "" {
		return imageGenerationRequestMetadata{}, fmt.Errorf("decode image generation request: model must not be blank")
	}
	if strings.TrimSpace(metadata.Prompt) == "" {
		return imageGenerationRequestMetadata{}, fmt.Errorf("decode image generation request: prompt must not be blank")
	}
	return metadata, nil
}

func invalidImageGenerationRequest(cause error) *model.ProxyError {
	return &model.ProxyError{
		Kind:    model.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: "invalid image generation request",
		Cause:   cause,
	}
}
