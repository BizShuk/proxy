package transform

import (
	"fmt"

	"github.com/bizshuk/proxy/protocol"
)

type pairKey struct {
	from protocol.Format
	to   protocol.Format
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

	for _, from := range protocol.ALL_FORMATS {
		for _, to := range protocol.ALL_FORMATS {
			if _, exists := registered[pairKey{from: from, to: to}]; !exists {
				return nil, fmt.Errorf("transform registry: missing pair %s -> %s", from, to)
			}
		}
	}
	return &Registry{pairs: registered}, nil
}

// Lookup returns a copy of one registered pair.
func (r *Registry) Lookup(from, to protocol.Format) (Pair, bool) {
	if r == nil {
		return Pair{}, false
	}
	pair, ok := r.pairs[pairKey{from: from, to: to}]
	return pair, ok
}
