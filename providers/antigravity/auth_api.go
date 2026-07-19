package antigravity

import "os"

const (
	// APIKeyEnvVar is the env var for a direct Antigravity API key path.
	// Some deployments expose an API key in addition to the OAuth flow.
	APIKeyEnvVar = "ANTIGRAVITY_API_KEY"

	// BaseURLEnvVar overrides the default Antigravity gateway URL.
	BaseURLEnvVar = "ANTIGRAVITY_BASE_URL"

	// DefaultBaseURL is Google's Antigravity gateway. Confirm against
	// https://help.router-for-me/configuration/provider/antigravity once
	// live — the gateway URL may differ.
	DefaultBaseURL = "https://antigravity.googleapis.com/v1"
)

// ResolveAPIKey returns the API key from the explicit value, then env.
// An empty result is not an error here — callers decide whether an API key
// is required (New() does, NewWithOAuth() does not).
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the base URL from the explicit value, then env,
// then the package default. The trailing slash is NOT trimmed here so the
// caller can decide whether to slice the path; provider.New() trims.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}