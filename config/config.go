package proxy

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bizshuk/proxy/svc/upstream"
	gosdkconfig "github.com/bizshuk/gosdk/config"
	"github.com/spf13/viper"
)

// APP_NAME is the gosdk config namespace the proxy loads settings from
// (~/.config/agentSDK). The auth CLI defaults to the "agentsdk" namespace;
// use auth-dir to point both at the same credential directory.
const APP_NAME = "agentSDK"

// Config is the fully-resolved runtime configuration, loaded through gosdk's
// layered viper loader (settings.json + settings.local.json, plus APP_*
// env override for keys whose dot→underscore form is a valid env name —
// i.e. anything without a `-`, such as server.port). Keys with
// dashes (auth-dir, body-limit-mb, timeouts.*-ms) are file-only.
// Field keys mirror the settings.json layout via mapstructure tags.
type Config struct {
	Server    ServerConfig           `mapstructure:"server"`
	AuthDir   string                 `mapstructure:"auth-dir"`
	APIKeys   []string               `mapstructure:"api-keys"`
	BodyLimit int                    `mapstructure:"body-limit-mb"`
	Timeouts  upstream.TimeoutConfig `mapstructure:"timeouts"`
	Stats     StatsConfig            `mapstructure:"stats"`
	Cloaking  map[string]any         `mapstructure:"cloaking"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type StatsConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// LoadConfig runs gosdk's config.Default (which merges settings.json →
// settings.local.json into the global viper, plus APP_* env override for
// keys whose flat form is a valid env name) and unmarshals the result
// into a typed Config, applying defaults for any unset field.
func LoadConfig() (*Config, error) {
	setDefaults()
	gosdkconfig.Default(gosdkconfig.WithAppName(APP_NAME))

	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.AuthDir = resolveAuthDir(cfg.AuthDir)
	if err := cfg.ensureAPIKey(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func setDefaults() {
	viper.SetDefault("server.host", "")
	viper.SetDefault("server.port", 8317)
	viper.SetDefault("auth-dir", "")
	viper.SetDefault("body-limit-mb", 200)
	viper.SetDefault("timeouts.messages-ms", 120000)
	viper.SetDefault("timeouts.stream-messages-ms", 600000)
	viper.SetDefault("timeouts.count-tokens-ms", 30000)
	viper.SetDefault("stats.enabled", true)
}

// ensureAPIKey mirrors the TS behaviour: if no api-keys are configured, mint
// one so a first run is usable. Persisting it is deferred to the login/serve
// commands (settings.local.json), keeping this package side-effect free.
func (c *Config) ensureAPIKey() error {
	if len(c.APIKeys) > 0 {
		return nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate api key: %w", err)
	}
	c.APIKeys = []string{"sk-" + hex.EncodeToString(buf)}
	return nil
}

func resolveAuthDir(dir string) string {
	if dir == "" {
		return filepath.Join(gosdkconfig.GetAppDataDir(), "auth")
	}
	if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(dir, "~"))
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// APIKeySet returns the configured keys as a lookup set for O(1) auth checks.
func (c *Config) APIKeySet() map[string]struct{} {
	set := make(map[string]struct{}, len(c.APIKeys))
	for _, k := range c.APIKeys {
		set[k] = struct{}{}
	}
	return set
}
