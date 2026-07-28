package upstream

const (
	OPENAI_REALTIME_CALLS_ENDPOINT          = "https://api.openai.com/v1/realtime/calls"
	OPENAI_REALTIME_CLIENT_SECRETS_ENDPOINT = "https://api.openai.com/v1/realtime/client_secrets"
	OPENAI_REALTIME_WEBSOCKET_ENDPOINT     = "wss://api.openai.com/v1/realtime"
)

// RealtimeTarget defines target metadata for an OpenAI Realtime session.
type RealtimeTarget struct {
	URL    string
	APIKey string
}
