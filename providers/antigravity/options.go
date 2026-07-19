package antigravity

type config struct {
	apiKey  string
	baseURL string
	model   string
}

// Option mutates the internal config in New / NewWithOAuth.
type Option func(*config)

func defaultConfig() config {
	return config{model: "claude-sonnet-5"}
}

// WithAPIKey sets the API key directly (bypasses ANTIGRAVITY_API_KEY env
// lookup). Required by New(); NewWithOAuth() ignores it.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL overrides the default Antigravity gateway URL (bypasses
// ANTIGRAVITY_BASE_URL env lookup).
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithModel picks the Antigravity-served model id. Defaults to
// "claude-sonnet-5".
func WithModel(m string) Option { return func(c *config) { c.model = m } }