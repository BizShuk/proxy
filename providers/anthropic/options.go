package anthropic

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/spf13/viper"
)

type config struct {
	apiKey  string
	model   anthropic.Model
	baseURL string
}

type Option func(*config)

func defaultConfig() config {
	return config{model: "claude-3-5-sonnet-latest"}
}

// WithAPIKey overrides the ANTHROPIC_API_KEY env lookup.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithModel picks the Claude model id.
func WithModel(m string) Option {
	return func(c *config) { c.model = anthropic.Model(m) }
}

// WithBaseURL sets a custom API endpoint (e.g. a proxy or gateway).
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithViper reads configuration keys from a viper instance to fill in
// gaps before explicit With*() options. Keys:
//
//	anthropic.api_key
//	anthropic.model
//	anthropic.base_url
//
// Example with gosdk config.Default(WithAppName("myapp")):
//
//	// ~/.config/myapp/settings.json:
//	// {"anthropic": {"api_key": "sk-...", "model": "claude-opus-4-8"}}
//
//	provider, _ := anthropic.New(anthropic.WithViper(viper.GetViper()))
//
// Place WithViper before explicit With*() options so the latter win:
//
//	anthropic.New(anthropic.WithViper(v), anthropic.WithModel("claude-haiku-4-5"))
func WithViper(v *viper.Viper) Option {
	return func(c *config) {
		if apiKey := v.GetString("anthropic.api_key"); apiKey != "" {
			c.apiKey = apiKey
		}
		if model := v.GetString("anthropic.model"); model != "" {
			c.model = anthropic.Model(model)
		}
		if baseURL := v.GetString("anthropic.base_url"); baseURL != "" {
			c.baseURL = baseURL
		}
	}
}