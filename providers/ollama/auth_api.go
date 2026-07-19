package ollama

import "os"

// APIKeyEnvVar is the optional Bearer token for protected servers
// (LM Studio with auth, vLLM with --api-key, OpenAI). Empty for local
// Ollama, which is key-less by default.
const APIKeyEnvVar = "OPENAI_API_KEY"

// BaseURLEnvVar overrides the default base URL. Local Ollama installs
// keep the default; this lets users point at LAN vLLM / OpenAI.
const BaseURLEnvVar = "OPENAI_BASE_URL"

// DefaultBaseURL points at a local Ollama instance.
const DefaultBaseURL = "http://localhost:11434/v1"

// ResolveAPIKey returns the explicit key, then the env var, else empty.
// An empty result means "keyless" — local Ollama runs without auth and
// the adapter skips the Authorization header entirely.
func ResolveAPIKey(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(APIKeyEnvVar)
}

// ResolveBaseURL returns the explicit URL, then the env var, then the
// local Ollama default. Trailing slashes are left alone here; the
// caller is responsible for normalizing.
func ResolveBaseURL(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(BaseURLEnvVar); v != "" {
		return v
	}
	return DefaultBaseURL
}