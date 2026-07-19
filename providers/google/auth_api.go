package google

import "os"

// APIKeyEnvVar is the required Bearer token for Google AI Studio.
// Empty API key fails construction in New(); there is no keyless path.
const APIKeyEnvVar = "GOOGLE_API_KEY"

// BaseURLEnvVar overrides the default base URL. Most users keep the
// default; this exists for proxying via Vertex AI or local mirrors.
const BaseURLEnvVar = "GOOGLE_BASE_URL"

// DefaultBaseURL points at Google Generative AI's OpenAI-compatible
// endpoint at /v1beta/openai.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai"

// ResolveAPIKey returns the explicit key, then the env var, else empty.
// An empty result surfaces as an "API key not set" error in New().
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the explicit URL, then the env var, then the
// public Google AI Studio default. Trailing slashes are left alone here;
// the caller is responsible for normalizing.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}