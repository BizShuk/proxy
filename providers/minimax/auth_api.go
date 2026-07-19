package minimax

import "os"

// minimax accepts long-lived API keys only — no OAuth. The auth header
// uses Anthropic's convention `x-api-key: <key>` rather than the more
// common `Authorization: Bearer` because the underlying endpoint is an
// Anthropic-Messages-compat surface.

// APIKeyEnvVar is the standard env var for a minimax API key.
const APIKeyEnvVar = "MINIMAX_API_KEY"

// BaseURLEnvVar lets operators point at a self-hosted or proxy-fronted
// minimax endpoint without recompiling.
const BaseURLEnvVar = "MINIMAX_BASE_URL"

// DefaultBaseURL is the public minimax Anthropic-compat endpoint.
const DefaultBaseURL = "https://api.minimax.io/anthropic"

// ResolveAPIKey returns the API key from the explicit value, then env.
// Explicit arg wins so tests and programmatic callers stay in control.
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the base URL from the explicit value, then env,
// then the public default. Order matches ResolveAPIKey for predictability.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}
