package upstream

// TimeoutConfig carries the per-endpoint upstream timeout policy in
// milliseconds. Field keys mirror the settings.json layout via mapstructure
// tags; the proxy config embeds it under "timeouts".
type TimeoutConfig struct {
	MessagesMs       int `mapstructure:"messages_ms"`
	StreamMessagesMs int `mapstructure:"stream_messages_ms"`
	CountTokensMs    int `mapstructure:"count_tokens_ms"`
}
