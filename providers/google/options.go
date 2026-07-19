package google

import "github.com/spf13/viper"

// config holds Provider construction-time options.
type config struct {
	baseURL string
	apiKey  string
	model   string
}

// Option mutates config during New.
type Option func(*config)

// defaultConfig returns the construction-time defaults. The default
// model is gemini-3-flash-preview because it's the most recent stable
// Gemini Flash release.
func defaultConfig() config {
	return config{model: "gemini-3-flash-preview"}
}

// WithBaseURL overrides the chat-completions endpoint. Empty string
// means "use the env var / default". Example:
//
//	google.New(google.WithBaseURL("https://generativelanguage.googleapis.com/v1beta/openai"))
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithAPIKey sets the bearer token (required for Google AI Studio).
// Falls back to GOOGLE_API_KEY env when empty.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithModel picks the model id passed to /chat/completions. Any string
// is accepted; the upstream decides whether it knows it.
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithViper reads configuration keys from a viper instance to fill in
// gaps before explicit With*() options. Keys:
//
//	google.base_url
//	google.api_key
//	google.model
//
// Place WithViper before explicit With*() options so the latter win:
//
//	google.New(google.WithViper(v), google.WithModel("gemini-3.5-flash"))
func WithViper(v *viper.Viper) Option {
	return func(c *config) {
		if u := v.GetString("google.base_url"); u != "" {
			c.baseURL = u
		}
		if k := v.GetString("google.api_key"); k != "" {
			c.apiKey = k
		}
		if m := v.GetString("google.model"); m != "" {
			c.model = m
		}
	}
}
