package transform

import "sync"

// MAX_ANTIGRAVITY_SIGNATURES bounds the replay cache. Each entry is one tool
// call's signature, so this covers a long agent session many times over while
// keeping the memory ceiling fixed.
const MAX_ANTIGRAVITY_SIGNATURES = 4096

// antigravitySignatureCache remembers the thought signature the gateway issued
// for each tool call.
//
// Gemini 3 rejects any replayed functionCall that arrives without its original
// thoughtSignature, but Anthropic Messages has no field for it on a tool_use
// block. The proxy writes the signature onto the block anyway, so clients that
// round-trip unknown fields need nothing more; this cache is the fallback for
// clients that strip them, which is most of them.
//
// It is deliberately keyed by tool_use ID alone: that ID is minted by the
// gateway, handed to the client, and echoed back verbatim in the next turn, so
// it survives the round-trip through a lossy client for free.
//
// This is the one piece of cross-request state the transform layer holds. It
// cannot live in a request-scoped Exchange because producer and consumer are
// different requests, and it cannot live in svc/upstream because that layer
// never sees the decoded response.
type antigravitySignatureCache struct {
	mu      sync.Mutex
	entries map[string]string
	order   []string
	limit   int
}

func newAntigravitySignatureCache(limit int) *antigravitySignatureCache {
	return &antigravitySignatureCache{
		entries: make(map[string]string, limit),
		limit:   limit,
	}
}

// antigravityToolSignatures is the process-wide replay cache.
var antigravityToolSignatures = newAntigravitySignatureCache(MAX_ANTIGRAVITY_SIGNATURES)

// Remember stores one tool call's signature, evicting the oldest entry once the
// cache is full.
func (c *antigravitySignatureCache) Remember(callID, signature string) {
	if callID == "" || signature == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[callID]; exists {
		c.entries[callID] = signature
		return
	}
	if len(c.order) >= c.limit {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
	c.entries[callID] = signature
	c.order = append(c.order, callID)
}

// Lookup returns the remembered signature, or empty when the call is unknown.
func (c *antigravitySignatureCache) Lookup(callID string) string {
	if callID == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[callID]
}
