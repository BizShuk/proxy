package adaptor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bizshuk/agentsdk/auth"
	"github.com/bizshuk/agentsdk/config"
)

func TestGetProviderForModel(t *testing.T) {
	tests := []struct {
		model    string
		provider string
	}{
		{"claude-3-5-sonnet", "anthropic"},
		{"claude-3-opus-20240229", "anthropic"},
		{"gpt-4o", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"grok-beta", "xai"},
		{"minimax-m3", "minimax"},
		{"MiniMax-Text-01", "minimax"},
		{"unknown-model", "anthropic"}, // fallback default
	}

	for _, tc := range tests {
		got := getProviderForModel(tc.model)
		if got != tc.provider {
			t.Errorf("model %q: expected provider %q, got %q", tc.model, tc.provider, got)
		}
	}
}

func TestLoadCredentialActiveSelection(t *testing.T) {
	// Create a temp directory for FileStore
	tempDir, err := os.MkdirTemp("", "agentsdk-test-auth-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	store, err := auth.NewFileStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create two credentials for same provider (anthropic)
	c1 := &auth.Credential{
		Provider: "anthropic",
		Kind:     auth.KIND_API_KEY,
		Account:  "user1@example.com",
		APIKey:   "key-user1",
	}
	c2 := &auth.Credential{
		Provider: "anthropic",
		Kind:     auth.KIND_API_KEY,
		Account:  "user2@example.com",
		APIKey:   "key-user2",
	}

	if err := store.Save(c1); err != nil {
		t.Fatalf("failed to save c1: %v", err)
	}
	if err := store.Save(c2); err != nil {
		t.Fatalf("failed to save c2: %v", err)
	}

	cfg := &config.ProxyConfig{
		AuthDir: tempDir,
	}
	a, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create adaptor: %v", err)
	}

	// 1. By default (no active.json), it should return first listed credential (alphabetically user1)
	cred, err := a.loadCredential("anthropic")
	if err != nil {
		t.Fatalf("failed to load default credential: %v", err)
	}
	if cred.Account != "user1@example.com" {
		t.Errorf("expected user1@example.com, got %s", cred.Account)
	}

	// 2. Set c2 as active in active.json
	active := map[string]string{
		"anthropic": c2.Name(),
	}
	payload, _ := json.Marshal(active)
	if err := os.WriteFile(filepath.Join(tempDir, "active.json"), payload, 0600); err != nil {
		t.Fatalf("failed to write active.json: %v", err)
	}

	// 3. Should now load c2
	cred, err = a.loadCredential("anthropic")
	if err != nil {
		t.Fatalf("failed to load active credential: %v", err)
	}
	if cred.Account != "user2@example.com" {
		t.Errorf("expected user2@example.com, got %s", cred.Account)
	}
}
