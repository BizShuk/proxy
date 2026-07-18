package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequireAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	keys := map[string]struct{}{
		"test-key-1": {},
		"test-key-2": {},
	}

	tests := []struct {
		name           string
		setupHeaders   func(req *http.Request)
		expectedStatus int
		expectLog      bool
		expectedReason string
	}{
		{
			name: "Valid x-api-key",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("x-api-key", "test-key-1")
			},
			expectedStatus: http.StatusOK,
			expectLog:      false,
		},
		{
			name: "Valid Authorization Bearer",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer test-key-2")
			},
			expectedStatus: http.StatusOK,
			expectLog:      false,
		},
		{
			name: "Missing API Key",
			setupHeaders: func(req *http.Request) {
				// No headers
			},
			expectedStatus: http.StatusUnauthorized,
			expectLog:      true,
			expectedReason: "missing API key",
		},
		{
			name: "Invalid API Key",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("x-api-key", "wrong-key")
			},
			expectedStatus: http.StatusForbidden,
			expectLog:      true,
			expectedReason: "invalid API key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))
			oldLogger := slog.Default()
			slog.SetDefault(logger)
			defer slog.SetDefault(oldLogger)

			router := gin.New()
			router.Use(requireAPIKey(keys))
			router.GET("/test", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tc.setupHeaders != nil {
				tc.setupHeaders(req)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)
			logOutput := logBuf.String()
			if tc.expectLog {
				assert.Contains(t, logOutput, "auth failed")
				assert.Contains(t, logOutput, tc.expectedReason)
				assert.Contains(t, logOutput, "ip=")
				assert.Contains(t, logOutput, "path=/test")
			} else {
				assert.Empty(t, logOutput)
			}
		})
	}
}
