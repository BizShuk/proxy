// Package upstream — dispatcher.go
//
// The Dispatcher is the proxy's bottom-layer entry into the agentsdk
// provider registry. Each provider family (anthropic, ollama, grok,
// antigravity, codex, minimax, google) owns its own DTO / validation /
// auth / stream parsing inside agentsdk; the Dispatcher holds the live
// provider.Adapter instances keyed by the agentsdk REGISTRY NAME so
// handler.go can route a request to the right family and pick up the
// model catalog without going through the legacy Profile/Catalog
// registry.
//
// Identity is passed in rather than read off the adapter. agentsdk's
// core.Provider is a pure capability interface (Generate / Stream) and
// deliberately carries no ID() or Models() — a name belongs to the
// registry that indexes adapters, not to the adapter itself. The
// dispatcher therefore keys on the same string provider.Lookup uses,
// and reads catalogs from provider.Catalog.
//
// The legacy Profile/Catalog in this package still exists because it
// carries per-provider wire-format metadata (Endpoints map, header
// allowlists) that the proxy's transform layer needs and agentsdk has
// no equivalent for. That surface stays proxy-owned.
package upstream

import (
	"errors"
	"fmt"
	"sync"

	"github.com/bizshuk/agentsdk/provider"
)

// Dispatcher holds the live provider.Adapter instances the proxy can
// dispatch to. It is concurrency-safe — call Lookup / Set from any
// goroutine.
type Dispatcher struct {
	mu       sync.RWMutex
	families map[string]provider.Adapter // key: agentsdk registry name ("anthropic", "grok", ...)
}

// NewDispatcher returns an empty Dispatcher. Use Set to register
// adapters, or NewDefaultDispatcher / NewDispatcherWithAuth for the
// common wiring paths.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{families: make(map[string]provider.Adapter)}
}

// Set registers the adapter for the given agentsdk registry name.
// Returns an error when the name is blank or already registered.
func (d *Dispatcher) Set(name string, p provider.Adapter) error {
	if d == nil || p == nil {
		return fmt.Errorf("dispatcher: nil provider")
	}
	if name == "" {
		return fmt.Errorf("dispatcher: provider name is blank")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.families[name]; exists {
		return fmt.Errorf("dispatcher: duplicate provider %q", name)
	}
	d.families[name] = p
	return nil
}

// Replace swaps the adapter for the given registry name.
//
// Token rotation no longer needs this: adapters built through
// NewDispatcherWithAuth carry a provider.Decorator that re-resolves the
// credential on every call. Replace remains for tests and for swapping
// an adapter whose non-credential config changed.
func (d *Dispatcher) Replace(name string, p provider.Adapter) error {
	if d == nil || p == nil {
		return fmt.Errorf("dispatcher: nil provider")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.families[name] = p
	return nil
}

// Lookup returns the adapter registered under the given registry name.
// The second return is false when no adapter is registered.
func (d *Dispatcher) Lookup(name string) (provider.Adapter, bool) {
	if d == nil {
		return nil, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.families[name]
	return p, ok
}

// IDs returns the registered agentsdk registry names.
func (d *Dispatcher) IDs() []string {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.families))
	for name := range d.families {
		out = append(out, name)
	}
	return out
}

// AdvertisedModels returns the union of every registered family's
// bundled catalog. This is what /v1/models returns when the proxy
// serves a flat model list.
//
// The catalog comes from provider.Catalog — the adapter itself no
// longer enumerates models, and the registry answers without any
// credential or network I/O. Families registered under a name agentsdk
// does not know contribute nothing. Order is non-deterministic;
// callers that care should sort.
func (d *Dispatcher) AdvertisedModels() []string {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	seen := make(map[string]struct{})
	var out []string
	for name := range d.families {
		specs, ok := provider.Catalog(name)
		if !ok {
			continue
		}
		for _, m := range specs {
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
