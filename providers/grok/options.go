// Functional options for grok.New / grok.NewWithOAuth.

package grok

// config holds Provider construction-time options.
type config struct {
	apiKey  string
	baseURL string
	model   string
}

// Option mutates config during New / NewWithOAuth.
type Option func(*config)

// defaultConfig seeds the default model. Resolution of apiKey and baseURL
// happens inside New / NewWithOAuth so env vars are consulted.
func defaultConfig() config {
	return config{model: "grok-3"}
}

// WithAPIKey overrides the XAI_API_KEY env lookup.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL overrides the XAI_BASE_URL env lookup. Useful for
// corporate proxies or local mocks.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithModel picks the model id passed to /chat/completions.
func WithModel(m string) Option { return func(c *config) { c.model = m } }