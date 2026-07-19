package anthropic

import "os"

// APIKeyEnvVar is the standard env var for an Anthropic API key.
const APIKeyEnvVar = "ANTHROPIC_API_KEY"

// APIKeyOAuthEnvVar is the env var for a pre-issued OAuth access token
// (used by Claude Code CLI forwarding and third-party proxies).
const APIKeyOAuthEnvVar = "ANTHROPIC_OAUTH_TOKEN"

// ResolveAPIKey returns the API key from the explicit value, then env.
// OAuth token takes precedence over API key when both are set — matches
// pi/ai's anthropicProvider auth order so behavior is predictable for
// users who run both an api key env and an oauth forwarding env.
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(APIKeyOAuthEnvVar); v != "" {
		return v
	}
	return os.Getenv(APIKeyEnvVar)
}

// IsOAuth reports whether the resolved key looks like an OAuth token.
// Heuristic: OAuth tokens issued by Anthropic's token endpoint are
// significantly longer than a typical sk-ant-... key (>100 chars). Not
// perfect but covers the common case without a separate flag.
func IsOAuth(key string) bool {
	return len(key) > 100
}
