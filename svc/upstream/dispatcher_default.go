// Package upstream — dispatcher_default.go
//
// Default Dispatcher wiring: the env-var path, with no auth store.
//
// This file used to import all seven agentsdk provider packages and
// call each one's New() by hand. It no longer names a single provider:
// adapters register themselves from their package init(), the blank
// import of provider/all in dispatcher_oauth.go links them, and
// provider.Names() enumerates whatever the binary linked. Adding an
// eighth family to agentsdk needs no change here.
//
// Auth note: provider.New resolves credentials from each provider's own
// Metadata (OAuthEnv / APIKeyEnv / BaseURLEnv) and returns an error when
// nothing is set. The dispatcher does not second-guess that — a family
// that fails to construct is skipped, and a dispatcher with zero
// families is still valid; handler.go surfaces credential_unavailable
// only when a request actually needs that family.
package upstream

import (
	"fmt"

	"github.com/bizshuk/agentsdk/provider"
)

// NewDefaultDispatcher returns a Dispatcher with every registered
// provider family whose credential resolves from the environment.
// Families whose env vars are not set fail provider.New and are
// skipped.
//
// Registration order follows provider.Names(), which is sorted — so
// Dispatcher.IDs() is deterministic for tests without this file
// carrying its own list.
func NewDefaultDispatcher() (*Dispatcher, error) {
	d := NewDispatcher()
	for _, name := range provider.Names() {
		p, err := provider.New(name, provider.Options{})
		if err != nil {
			// Skip families that can't be constructed (env not set,
			// etc.). They can be added later via Dispatcher.Set after a
			// runtime auth handshake.
			continue
		}
		if err := d.Set(name, p); err != nil {
			return nil, fmt.Errorf("new default dispatcher: register %s: %w", name, err)
		}
	}
	return d, nil
}
