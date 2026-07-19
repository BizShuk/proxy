package minimax

import "github.com/spf13/viper"

// config holds Provider construction-time options.
type config struct {
	apiKey  string
	baseURL string
	model   string
}

// Option mutates config during New.
type Option func(*config)

// defaultConfig returns the built-in defaults. model defaults to
// minimax-M2 (current flagship).
func defaultConfig() config {
	return config{model: "minimax-M2"}
}

// WithAPIKey overrides the MINIMAX_API_KEY env lookup.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithBaseURL sets a custom API endpoint (e.g. a proxy or gateway).
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithModel picks the model id passed to /v1/messages.
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithViper reads configuration keys from a viper instance to fill in
// gaps before explicit With*() options. Keys:
//
//	minimax.api_key
//	minimax.base_url
//	minimax.model
//
// Example with gosdk config.Default(WithAppName("myapp")):
//
//	// ~/.config/myapp/settings.json:
//	// {"minimax": {"api_key": "...", "model": "minimax-M2"}}
//
//	provider, _ := minimax.New(minimax.WithViper(viper.GetViper()))
//
// Place WithViper before explicit With*() options so the latter win:
//
//	minimax.New(minimax.WithViper(v), minimax.WithModel("MiniMax-Text-01"))
func WithViper(v *viper.Viper) Option {
	return func(c *config) {
		if k := v.GetString("minimax.api_key"); k != "" {
			c.apiKey = k
		}
		if u := v.GetString("minimax.base_url"); u != "" {
			c.baseURL = u
		}
		if m := v.GetString("minimax.model"); m != "" {
			c.model = m
		}
	}
}
