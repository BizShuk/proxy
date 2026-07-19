// Auth helpers for the API-key (long-lived secret) flavor of xAI Grok.
//
// xAI documents this flow at https://docs.x.ai/docs/authentication —
// generate a key in the xAI console and pass it via the XAI_API_KEY env
// var (or the WithAPIKey option).

package grok

import "os"

const (
	// APIKeyEnvVar is the standard env var for an xAI API key.
	APIKeyEnvVar = "XAI_API_KEY"

	// BaseURLEnvVar is the standard env var for overriding the Grok API
	// endpoint (e.g. for a corporate proxy or local mock).
	BaseURLEnvVar = "XAI_BASE_URL"

	// DefaultBaseURL is the public xAI chat-completions endpoint.
	DefaultBaseURL = "https://api.x.ai/v1"
)

// ResolveAPIKey returns the API key from the explicit value, then env.
// Empty result means neither was set; the caller (New) returns an error
// in that case so callers get a clear "missing credential" diagnostic.
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the base URL from the explicit value, then env,
// then the public default. Trailing slashes are not trimmed here —
// callers that care should strip them at construction time.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}