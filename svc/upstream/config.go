package upstream

// TimeoutConfig carries the per-endpoint upstream timeout policy in
// milliseconds. Field keys mirror the settings.json layout via mapstructure
// tags; the proxy config embeds it under "timeouts".
type TimeoutConfig struct {
	MessagesMs       int `mapstructure:"messages-ms"`
	StreamMessagesMs int `mapstructure:"stream-messages-ms"`
	CountTokensMs    int `mapstructure:"count-tokens-ms"`
}
