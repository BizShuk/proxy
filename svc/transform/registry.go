package transform

import (
	"fmt"

	"github.com/bizshuk/proxy/model"
)

type pairKey struct {
	from model.Format
	to   model.Format
}

// Registry is an immutable complete matrix of directed protocol pairs.
type Registry struct {
	pairs map[pairKey]Pair
}

// NewRegistry validates and copies a complete transform matrix.
func NewRegistry(pairs ...Pair) (*Registry, error) {
	registered := make(map[pairKey]Pair, len(pairs))
	for index, pair := range pairs {
		if !pair.From.Valid() || !pair.To.Valid() {
			return nil, fmt.Errorf("transform registry pair %d: unknown format %q -> %q", index, pair.From, pair.To)
		}
		key := pairKey{from: pair.From, to: pair.To}
		if _, exists := registered[key]; exists {
			return nil, fmt.Errorf("transform registry: duplicate pair %s -> %s", pair.From, pair.To)
		}
		if pair.Request == nil {
			return nil, fmt.Errorf("transform registry pair %s -> %s: nil request transform", pair.From, pair.To)
		}
		if pair.Response == nil {
			return nil, fmt.Errorf("transform registry pair %s -> %s: nil response transform", pair.From, pair.To)
		}
		if pair.NewStream == nil {
			return nil, fmt.Errorf("transform registry pair %s -> %s: nil stream factory", pair.From, pair.To)
		}
		registered[key] = pair
	}

	for _, from := range model.CLIENT_FORMATS {
		if !from.ClientFacing() {
			continue
		}
		for _, to := range model.CLIENT_FORMATS {
			if _, exists := registered[pairKey{from: from, to: to}]; !exists {
				return nil, fmt.Errorf("transform registry: missing pair %s -> %s", from, to)
			}
		}
	}
	// Provider-only formats are upstream targets, never client sources, so they
	// need no row. They do need at least one client able to reach them —
	// otherwise the format is dead weight that routing can never select.
	for _, to := range model.PROVIDER_FORMATS {
		reachable := false
		for _, from := range model.CLIENT_FORMATS {
			if _, exists := registered[pairKey{from: from, to: to}]; exists {
				reachable = true
				break
			}
		}
		if !reachable {
			return nil, fmt.Errorf("transform registry: provider format %s has no client source", to)
		}
	}
	for key := range registered {
		if !key.from.ClientFacing() {
			return nil, fmt.Errorf("transform registry: %s is provider-only and cannot be a source", key.from)
		}
	}
	return &Registry{pairs: registered}, nil
}

// Lookup returns a copy of one registered pair.
func (r *Registry) Lookup(from, to model.Format) (Pair, bool) {
	if r == nil {
		return Pair{}, false
	}
	pair, ok := r.pairs[pairKey{from: from, to: to}]
	return pair, ok
}
