package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/bizshuk/proxy/model"
	"github.com/gin-gonic/gin"
)

const maxImageEditFieldBytes int64 = 1 << 20

type imageEditRequestMetadata struct {
	Model       string
	Prompt      string
	HasImage    bool
	ContentType string
}

// HandleImageEdits returns an OpenAI-compatible multipart image-edit handler.
// It validates the required multipart fields, resolves the provider from the
// image model, and forwards the original bounded body without rewriting it.
func (h *Handler) HandleImageEdits() gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := h.readRequestBody(c)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, err)
			return
		}
		contentType := c.GetHeader("Content-Type")
		metadata, err := decodeImageEditRequest(contentType, body)
		if err != nil {
			h.writeError(c, model.FORMAT_OPENAI_RESPONSES, invalidImageEditRequest(err))
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

		slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "proxy image edit routed",
			slog.String("request_id", requestIDValue),
			slog.String("model", metadata.Model),
			slog.String("routed_family", providerFamily),
			slog.String("provider", profile.ID),
			slog.String("credential_kind", string(credential.Kind)),
		)
		startedAt := time.Now()
		defer func() {
			slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "proxy image edit completed",
				slog.String("request_id", requestIDValue),
				slog.String("model", metadata.Model),
				slog.String("provider", profile.ID),
				slog.Int("status", c.Writer.Status()),
				slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
			)
		}()

		headers := c.Request.Header.Clone()
		headers.Set("x-request-id", requestIDValue)
		response, err := h.client.EditImage(c.Request.Context(), profile, credential, model.RequestEnvelope{
			SourceFormat: model.FORMAT_OPENAI_RESPONSES,
			TargetFormat: model.FORMAT_OPENAI_RESPONSES,
			Model:        metadata.Model,
			Headers:      headers,
			ContentType:  metadata.ContentType,
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
			h.logUpstreamError(c.Request.Context(), requestIDValue, metadata.Model, profile.ID, response)
		}

		copySafeResponseHeaders(c.Writer.Header(), response.Header, profile)
		c.Header("x-request-id", requestIDValue)
		responseContentType := strings.TrimSpace(response.Header.Get("Content-Type"))
		if responseContentType == "" {
			responseContentType = "application/json"
		}
		c.Data(response.StatusCode, responseContentType, responseBody)
	}
}

func decodeImageEditRequest(contentType string, body []byte) (imageEditRequestMetadata, error) {
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return imageEditRequestMetadata{}, fmt.Errorf("content type must be multipart/form-data")
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return imageEditRequestMetadata{}, fmt.Errorf("multipart boundary is required")
	}

	metadata := imageEditRequestMetadata{ContentType: strings.TrimSpace(contentType)}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return imageEditRequestMetadata{}, fmt.Errorf("read multipart form: %w", nextErr)
		}

		switch part.FormName() {
		case "model":
			value, err := readImageEditField(part)
			if err != nil {
				return imageEditRequestMetadata{}, fmt.Errorf("read model: %w", err)
			}
			metadata.Model = value
		case "prompt":
			value, err := readImageEditField(part)
			if err != nil {
				return imageEditRequestMetadata{}, fmt.Errorf("read prompt: %w", err)
			}
			metadata.Prompt = value
		case "image":
			var firstByte [1]byte
			n, readErr := part.Read(firstByte[:])
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return imageEditRequestMetadata{}, fmt.Errorf("read image: %w", readErr)
			}
			metadata.HasImage = metadata.HasImage || n > 0
		}
	}

	if strings.TrimSpace(metadata.Model) == "" {
		return imageEditRequestMetadata{}, fmt.Errorf("model must not be blank")
	}
	if strings.TrimSpace(metadata.Prompt) == "" {
		return imageEditRequestMetadata{}, fmt.Errorf("prompt must not be blank")
	}
	if !metadata.HasImage {
		return imageEditRequestMetadata{}, fmt.Errorf("image must not be empty")
	}
	return metadata, nil
}

func readImageEditField(part *multipart.Part) (string, error) {
	value, err := io.ReadAll(io.LimitReader(part, maxImageEditFieldBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(value)) > maxImageEditFieldBytes {
		return "", fmt.Errorf("field exceeds %d bytes", maxImageEditFieldBytes)
	}
	return string(value), nil
}

func invalidImageEditRequest(cause error) *model.ProxyError {
	return &model.ProxyError{
		Kind:    model.ERROR_INVALID_REQUEST,
		Status:  http.StatusBadRequest,
		Code:    "invalid_request",
		Message: "invalid image edit request",
		Cause:   cause,
	}
}
