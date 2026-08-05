// Package mcpimage exposes the proxy image-generation endpoint as an MCP tool.
package mcpimage

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	sdkgrok "github.com/bizshuk/agentsdk/provider/grok"
)

const (
	DEFAULT_BASE_URL        = "http://127.0.0.1"
	DEFAULT_PORT            = 8317
	DEFAULT_MODEL           = sdkgrok.DefaultImageModel
	DEFAULT_OUTPUT_DIR      = "images"
	DEFAULT_REQUEST_TIMEOUT = 5 * time.Minute

	ENV_BASE_URL           = "PROXY_IMAGE_BASE_URL"
	ENV_PORT               = "PROXY_IMAGE_PORT"
	ENV_API_KEY            = "PROXY_IMAGE_API_KEY"
	ENV_AGENTSDK_API_KEY   = "AGENTSDK_PROXY_API_KEY"
	ENV_MODEL              = "PROXY_IMAGE_MODEL"
	ENV_OUTPUT_DIR         = "PROXY_IMAGE_OUTPUT_DIR"
	ENV_CLAUDE_PROJECT_DIR = "CLAUDE_PROJECT_DIR"
)

// Config defines how the MCP server reaches the proxy and stores generated
// images.
type Config struct {
	BaseURL    string
	Port       int
	APIKey     string
	Model      string
	OutputDir  string
	ProjectDir string
}

// LoadConfigFromEnv resolves MCP configuration from environment variables.
func LoadConfigFromEnv(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("load image MCP config: environment lookup is required")
	}

	cfg := Config{
		BaseURL:    envOrDefault(lookup, ENV_BASE_URL, DEFAULT_BASE_URL),
		Port:       DEFAULT_PORT,
		APIKey:     firstNonBlankEnv(lookup, ENV_API_KEY, ENV_AGENTSDK_API_KEY),
		Model:      envOrDefault(lookup, ENV_MODEL, DEFAULT_MODEL),
		OutputDir:  envOrDefault(lookup, ENV_OUTPUT_DIR, DEFAULT_OUTPUT_DIR),
		ProjectDir: strings.TrimSpace(envValue(lookup, ENV_CLAUDE_PROJECT_DIR)),
	}

	if rawPort := strings.TrimSpace(envValue(lookup, ENV_PORT)); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil {
			return Config{}, fmt.Errorf("load image MCP config %s: %w", ENV_PORT, err)
		}
		cfg.Port = port
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the proxy connection and image-output configuration.
func (c Config) Validate() error {
	if strings.TrimSpace(c.APIKey) == "" {
		return fmt.Errorf(
			"image MCP config: %s or %s is required",
			ENV_API_KEY,
			ENV_AGENTSDK_API_KEY,
		)
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("image MCP config: model must not be blank")
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		return fmt.Errorf("image MCP config: output directory must not be blank")
	}
	_, err := c.EndpointURL()
	return err
}

// EndpointURL builds the proxy's OpenAI-compatible image-generation endpoint.
func (c Config) EndpointURL() (string, error) {
	baseURL := strings.TrimSpace(c.BaseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("image MCP config: parse base URL: %w", err)
	}
	if !parsed.IsAbs() || parsed.Hostname() == "" {
		return "", fmt.Errorf("image MCP config: base URL must be absolute and include a host")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("image MCP config: base URL scheme must be http or https")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("image MCP config: base URL must not include userinfo")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("image MCP config: base URL must not include query or fragment")
	}
	if c.Port < 1 || c.Port > 65535 {
		return "", fmt.Errorf("image MCP config: port must be between 1 and 65535")
	}

	parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(c.Port))
	basePath := strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(basePath, "/v1") {
		parsed.Path = path.Join(basePath, "images/generations")
	} else {
		parsed.Path = path.Join(basePath, "v1/images/generations")
	}
	parsed.RawPath = ""
	return parsed.String(), nil
}

func envOrDefault(lookup func(string) (string, bool), key, fallback string) string {
	value := strings.TrimSpace(envValue(lookup, key))
	if value == "" {
		return fallback
	}
	return value
}

func firstNonBlankEnv(lookup func(string) (string, bool), keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(envValue(lookup, key)); value != "" {
			return value
		}
	}
	return ""
}

func envValue(lookup func(string) (string, bool), key string) string {
	value, _ := lookup(key)
	return value
}
