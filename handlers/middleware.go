package handlers

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// requireAPIKey accepts both OpenAI style (Authorization: Bearer) and
// Anthropic style (x-api-key) so Claude Code and OpenAI clients both work.
// Comparison is constant-time to avoid a timing oracle on the key.
func requireAPIKey(keys map[string]struct{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractAPIKey(c.Request)
		if key == "" {
			slog.Error("auth failed", "reason", "missing API key", "ip", c.ClientIP(), "path", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"message": "Missing API key"},
			})
			return
		}
		if !validKey(keys, key) {
			slog.Error("auth failed", "reason", "invalid API key", "ip", c.ClientIP(), "path", c.Request.URL.Path)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"message": "Invalid API key"},
			})
			return
		}
		c.Next()
	}
}

func extractAPIKey(r *http.Request) string {
	if v := r.Header.Get("x-api-key"); v != "" {
		return strings.TrimSpace(v)
	}
	if v := r.Header.Get("Authorization"); v != "" {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	return ""
}

func validKey(keys map[string]struct{}, key string) bool {
	// Range with constant-time compare: membership alone would leak via map
	// timing; subtle.ConstantTimeCompare keeps the check uniform per key.
	match := 0
	for k := range keys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			match = 1
		}
	}
	return match == 1
}

var localhostOrigin = regexp.MustCompile(`^https?://(localhost|127\.0\.0\.1)(:\d+)?$`)

// corsLocalhost restricts browser CORS to localhost origins only.
func corsLocalhost() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		if origin != "" && localhostOrigin.MatchString(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

const (
	rateLimitWindow = time.Minute
	rateLimitMax    = 60
)

type rateBucket struct {
	count   int
	resetAt time.Time
}

// rateLimitPerIP is a simple fixed-window per-IP limiter, mutex-guarded for
// concurrent access (the TS version relied on the single-threaded event loop).
func rateLimitPerIP() gin.HandlerFunc {
	var mu sync.Mutex
	buckets := make(map[string]*rateBucket)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()

		mu.Lock()
		b, ok := buckets[ip]
		if !ok || now.After(b.resetAt) {
			buckets[ip] = &rateBucket{count: 1, resetAt: now.Add(rateLimitWindow)}
			mu.Unlock()
			c.Next()
			return
		}
		b.count++
		over := b.count > rateLimitMax
		mu.Unlock()

		if over {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"message": "Too many requests"},
			})
			return
		}
		c.Next()
	}
}
