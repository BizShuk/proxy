// Package upstream — dispatcher_default.go
//
// Default Dispatcher wiring. Each provider package under
// github.com/bizshuk/agentsdk/provider/<name>/ is imported here and
// registered with the dispatcher.
//
// Auth note: each provider's New() looks at env vars (ANTHROPIC_API_KEY,
// XAI_API_KEY, etc.) and returns an error if none is set. The dispatcher
// does NOT validate auth at construction time — provider packages
// handle that. A dispatcher with zero providers (env not set) is
// still valid; handler.go will surface "credential_unavailable" only
// when a request actually needs that family.
package upstream

import (
	"github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/proxy/providers/antigravity"
	"github.com/bizshuk/proxy/providers/anthropic"
	"github.com/bizshuk/proxy/providers/codex"
	"github.com/bizshuk/proxy/providers/google"
	"github.com/bizshuk/proxy/providers/grok"
	"github.com/bizshuk/proxy/providers/minimax"
	"github.com/bizshuk/proxy/providers/ollama"
)

// NewDefaultDispatcher returns a Dispatcher with the canonical seven
// provider families registered. Providers whose env vars are not set
// fail their New() call and are skipped — the dispatcher still
// registers the families that succeed.
//
// The order of registration is fixed:
//
//	anthropic → ollama → grok → antigravity → codex → minimax → google
//
// Order is cosmetic (the dispatcher uses a map), but it makes
// dispatcher.IDs() deterministic for tests.
func NewDefaultDispatcher() (*Dispatcher, error) {
	d := NewDispatcher()
	candidates := []struct {
		name string
		build func() (core.Provider, error)
	}{
		{"anthropic", func() (core.Provider, error) {
			return anthropic.New()
		}},
		{"ollama", func() (core.Provider, error) {
			return ollama.New()
		}},
		{"grok", func() (core.Provider, error) {
			return grok.New()
		}},
		{"antigravity", func() (core.Provider, error) {
			return antigravity.New()
		}},
		{"codex", func() (core.Provider, error) {
			return codex.New()
		}},
		{"minimax", func() (core.Provider, error) {
			return minimax.New()
		}},
		{"google", func() (core.Provider, error) {
			return google.New()
		}},
	}
	for _, c := range candidates {
		p, err := c.build()
		if err != nil {
			// Skip providers that can't be constructed (env not set,
			// etc.). They can be added later via Dispatcher.Set after
			// a runtime auth handshake.
			continue
		}
		if err := d.Set(p); err != nil {
			return nil, err
		}
	}
	return d, nil
}