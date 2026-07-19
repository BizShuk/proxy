package ollama

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
// model is llama3.2 because it's a common local-Ollama install.
func defaultConfig() config {
	return config{model: "llama3.2"}
}

// WithBaseURL overrides the chat-completions endpoint. Empty string
// means "use the env var / default". Example:
//
//	ollama.New(ollama.WithBaseURL("http://localhost:11434/v1"))
//	ollama.New(ollama.WithBaseURL("https://api.openai.com/v1"))
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithAPIKey sets the bearer token (empty for local key-less hosts).
// LM Studio with auth, vLLM with --api-key, and OpenAI all accept
// Bearer; local Ollama ignores it.
func WithAPIKey(k string) Option { return func(c *config) { c.apiKey = k } }

// WithModel picks the model id passed to /chat/completions. Any string
// is accepted; the server decides whether it knows it.
func WithModel(m string) Option { return func(c *config) { c.model = m } }

// WithViper reads configuration keys from a viper instance to fill in
// gaps before explicit With*() options. Keys:
//
//	ollama.base_url
//	ollama.api_key
//	ollama.model
//
// Example with gosdk config.Default(WithAppName("myapp")):
//
//	// ~/.config/myapp/settings.json:
//	// {"ollama": {"base_url": "http://localhost:11434/v1", "model": "llama3.2"}}
//
//	provider, _ := ollama.New(ollama.WithViper(viper.GetViper()))
//
// Place WithViper before explicit With*() options so the latter win:
//
//	ollama.New(ollama.WithViper(v), ollama.WithModel("qwen3"))
func WithViper(v *viper.Viper) Option {
	return func(c *config) {
		if u := v.GetString("ollama.base_url"); u != "" {
			c.baseURL = u
		}
		if k := v.GetString("ollama.api_key"); k != "" {
			c.apiKey = k
		}
		if m := v.GetString("ollama.model"); m != "" {
			c.model = m
		}
	}
}