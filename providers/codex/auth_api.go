package codex

import "os"

// Codex is OAuth-first — most users authenticate via ChatGPT Plus/Pro
// OAuth credentials (see auth_oauth.go). The API key path is provided
// for completeness and for testing against a local mock that does not
// require the OAuth handshake.

const (
	// APIKeyEnvVar — placeholder; the Codex endpoint does not accept
	// arbitrary OpenAI keys. Most deployments should use the OAuth
	// credential flow instead.
	APIKeyEnvVar = "OPENAI_API_KEY"

	// BaseURLEnvVar — override the upstream base URL. Useful for
	// pointing tests at a local mock.
	BaseURLEnvVar = "CODEX_BASE_URL"

	// DefaultBaseURL is the production Codex endpoint. Code behind
	// the chatgpt.com boundary — there is no api.openai.com alias.
	DefaultBaseURL = "https://chatgpt.com/backend-api"
)

// ResolveAPIKey returns the API key from the explicit value, then the
// environment. The Codex endpoint expects OAuth bearer tokens, so this
// resolution path is mostly for tests and the placeholder fallback.
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the upstream base URL: explicit override,
// then CODEX_BASE_URL, then the production default.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}
