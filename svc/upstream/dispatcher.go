// Package upstream — dispatcher.go
//
// The Dispatcher is the proxy's bottom-layer entry into the agentsdk
// provider packages. Each provider family (anthropic, ollama, grok,
// antigravity, codex, minimax) owns its own DTO / validation / auth /
// stream parsing under llm_provider/<name>/. The Dispatcher holds
// live provider.Provider instances keyed by family so handler.go can
// route a request to the right provider and pick up per-provider
// metadata (model catalog, supported auth schemes) without going
// through the legacy Profile/Catalog registry.
//
// The legacy Profile/Catalog in this package still exists because it
// carries per-provider wire-format metadata (Endpoints map, header
// allowlists) that the proxy's transform layer needs. Future work
// will collapse that surface into provider-aware format adapters; for
// now the Dispatcher lives ALONGSIDE the Catalog.
package upstream

import (
	"errors"
	"fmt"
	"sync"

	"github.com/bizshuk/agentsdk/core"
)

// ProviderBuilder constructs a provider.Provider from a credential kind
// and resolved secret. It is the seam between the proxy's credential
// resolver and the provider's own auth flow:
//
//	api_key  → pass the resolved key to provider.New(WithAPIKey(key))
//	oauth    → pass OAuthCredentials to provider.NewWithOAuth(creds)
//
// Returning an error here surfaces as a credential_unavailable proxy
// error at handler.go.
type ProviderBuilder func(creds ResolvedCredential) (core.Provider, error)

// Dispatcher holds the live provider.Provider instances the proxy can
// dispatch to. It is concurrency-safe — call Lookup / Set from any
// goroutine.
type Dispatcher struct {
	mu       sync.RWMutex
	families map[string]core.Provider // key: family id ("anthropic", "ollama", ...)
}

// NewDispatcher returns an empty Dispatcher. Use Set to register
// providers, or NewDispatcherFromProviders for the common case of
// constructing every provider up front.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{families: make(map[string]core.Provider)}
}

// NewDispatcherFromProviders registers a static set of providers keyed
// by their ID(). Returns an error if two providers share an ID.
func NewDispatcherFromProviders(providers ...core.Provider) (*Dispatcher, error) {
	d := NewDispatcher()
	for _, p := range providers {
		if p == nil {
			return nil, fmt.Errorf("dispatcher: nil provider")
		}
		if err := d.Set(p); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Set registers (or replaces) the provider for the given family id.
func (d *Dispatcher) Set(p core.Provider) error {
	if d == nil || p == nil {
		return fmt.Errorf("dispatcher: nil provider")
	}
	id := p.ID()
	if id == "" {
		return fmt.Errorf("dispatcher: provider ID is blank")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.families[id]; exists {
		return fmt.Errorf("dispatcher: duplicate provider %q", id)
	}
	d.families[id] = p
	return nil
}

// Replace swaps the provider for the given family id. Used by the
// refreshing-provider flow when a token rotation rebuilds the inner.
func (d *Dispatcher) Replace(id string, p core.Provider) error {
	if d == nil || p == nil {
		return fmt.Errorf("dispatcher: nil provider")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.families[id] = p
	return nil
}

// Lookup returns the provider registered under the given family id.
// The second return is false when no provider is registered.
func (d *Dispatcher) Lookup(family string) (core.Provider, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.families[family]
	return p, ok
}

// IDs returns the sorted list of registered family ids.
func (d *Dispatcher) IDs() []string {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.families))
	for id := range d.families {
		out = append(out, id)
	}
	return out
}

// AdvertisedModels returns the union of every registered provider's
// static catalog. This is what /v1/models returns when the proxy
// serves a flat model list. Order is non-deterministic; callers that
// care should sort.
func (d *Dispatcher) AdvertisedModels() []string {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []string
	for _, p := range d.families {
		for _, m := range p.Models() {
			if _, ok := seen[m.ID]; ok {
				continue
			}
			seen[m.ID] = struct{}{}
			out = append(out, m.ID)
		}
	}
	return out
}

// ErrUnknownFamily is returned when the dispatcher has no provider for
// the requested family. Handler maps this to a 404 unknown_provider.
var ErrUnknownFamily = errors.New("dispatcher: unknown provider family")
