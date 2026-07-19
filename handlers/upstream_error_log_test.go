package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterResponseHeaders_RemovesSensitiveKeys(t *testing.T) {
	in := http.Header{}
	in.Set("Authorization", "Bearer secret")
	in.Set("Set-Cookie", "sid=abc; HttpOnly")
	in.Set("X-API-Key", "sk-123")
	in.Set("Retry-After", "30")
	in.Set("X-Request-ID", "req-1")
	in.Add("X-Custom", "v1")
	in.Add("X-Custom", "v2")

	out := filterResponseHeaders(in)

	assert.Equal(t, "30", out.Get("Retry-After"))
	assert.Equal(t, "req-1", out.Get("X-Request-ID"))
	assert.Equal(t, []string{"v1", "v2"}, out.Values("X-Custom"))
	assert.Empty(t, out.Get("Authorization"))
	assert.Empty(t, out.Get("Set-Cookie"))
	assert.Empty(t, out.Get("X-API-Key"))
}

func TestFilterResponseHeaders_NilInputReturnsEmpty(t *testing.T) {
	out := filterResponseHeaders(nil)
	assert.NotNil(t, out)
	assert.Empty(t, out)
}
