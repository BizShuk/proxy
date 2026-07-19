package codex

// Options shape the Provider at construction time. Most fields have
// a sane default and rarely need to be overridden except for tests
// (WithBaseURL pointing at httptest.Server) and for picking a
// non-default model (WithModel).

// config is the internal bag of all knobs New accepts. The Option
// type mutates a config in place so callers can chain options.
type config struct {
	apiKey    string
	baseURL   string
	model     string
	accountID string
}

type Option func(*config)

// defaultConfig seeds the defaults — gpt-5 model and the default
// Codex upstream URL.
func defaultConfig() config {
	return config{model: "gpt-5"}
}

// WithAPIKey sets a long-lived OpenAI API key. Most deployments use
// NewWithOAuth instead; this exists for tests and the placeholder
// fallback path.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL sets a custom upstream base URL. The provider appends
// "/codex/responses" to whatever URL you give.
//
// Useful for:
//
//   - pointing tests at httptest.Server
//   - routing through an internal gateway
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithModel picks the model id (e.g. "gpt-5", "gpt-5-mini", "gpt-5.6").
// Defaults to "gpt-5".
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithAccountID sets the ChatGPT-Account-ID header value. The header
// is omitted when no account id is available (the auth server
// infers it from the bearer token in that case).
func WithAccountID(id string) Option { return func(c *config) { c.accountID = id } }
