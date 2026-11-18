package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// settings.json 的 snake_case key 與 .env 的 SCREAMING_SNAKE credential 都要
// 解進 Config: API_KEYS 以逗號分隔成多把 key.
func TestLoadConfigSnakeSettingsAndEnvCredentials(t *testing.T) {
	dir := t.TempDir()
	settings := `{"auth_dir":"/tmp/proxy-auth","body_limit_mb":7,"timeouts":{"messages_ms":11},"realtime":{"max_connections":3}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_KEYS=sk-a,sk-b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuthDir != "/tmp/proxy-auth" || cfg.BodyLimit != 7 || cfg.Timeouts.MessagesMs != 11 || cfg.Realtime.MaxConnections != 3 {
		t.Fatalf("snake_case settings not decoded: %+v", cfg)
	}
	if len(cfg.APIKeys) != 2 || cfg.APIKeys[0] != "sk-a" || cfg.APIKeys[1] != "sk-b" {
		t.Fatalf("APIKeys = %v, want [sk-a sk-b]", cfg.APIKeys)
	}
}
