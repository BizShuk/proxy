// Package upstream — dispatcher_oauth.go
//
// Auth-backed Dispatcher wiring. This file used to carry a per-provider
// routing table: a switch mapping each family to its own constructor
// (anthropic.NewWithOAuth, codex.NewWithOAuth, ...), a hand-written
// authmodel.Credential → OAuthCredentials adapter, and the xai → grok
// name mapping between the auth storage id and the SDK provider id.
//
// All three now belong to agentsdk and are gone from here:
//
//	proxy (before)                    agentsdk (now)
//	------------------------------    ---------------------------------
//	buildAPIKeyProvider switch        provider.New(name, Options)
//	buildOAuthProvider switch         provider.New(name, Options)
//	authmodelToOAuth projection       credential.Source.Decorator()
//	"xai" → "grok" mapping            credential.RouteID(name, kind)
//	familiesInDefaultOrder() list     credential.Names() / provider.Names()
//
// The credential is no longer snapshotted at construction time. Each
// adapter carries a provider.Decorator that re-resolves through the
// auth store on every call, so a rotated token is picked up without
// rebuilding the adapter — Dispatcher.Replace is no longer part of the
// token-refresh path.
package upstream

import (
	"context"
	"fmt"

	"github.com/bizshuk/agentsdk/provider"
	"github.com/bizshuk/agentsdk/provider/credential"

	// Link every adapter so the registry is populated. agentsdk imports
	// no adapter from provider/ itself; the binary decides the set.
	_ "github.com/bizshuk/agentsdk/provider/all"
)

// NewDispatcherWithAuth builds a Dispatcher from the auth store. Every
// provider agentsdk can route to an auth credential (credential.Names)
// is probed; families whose credential is absent or unusable are
// silently skipped, so the dispatcher stays usable for the rest.
//
// This is the production wiring path: `auth login --provider X` saves
// credentials; the proxy picks them up at startup and re-reads them on
// every call through the decorator.
func NewDispatcherWithAuth(store credential.Store) (*Dispatcher, error) {
	return newDispatcherWithAuth(store, credential.Names())
}

// NewDispatcherWithAuthAndEnv is the fallback path: it wires the auth
// store first, then fills in any remaining registered provider from the
// environment. Useful during dev / when only some providers have
// completed OAuth login.
//
// Note: the env fallback always resolves api_key-style credentials —
// agentsdk's Options.Resolve reads the provider's own OAuthEnv/APIKeyEnv
// metadata, so the precedence rules live with the provider, not here.
func NewDispatcherWithAuthAndEnv(store credential.Store) (*Dispatcher, error) {
	d, err := newDispatcherWithAuth(store, credential.Names())
	if err != nil {
		return nil, err
	}
	for _, name := range provider.Names() {
		if _, ok := d.Lookup(name); ok {
			continue
		}
		p, err := provider.New(name, provider.Options{})
		if err != nil {
			// No env credential for this family — skip.
			continue
		}
		if err := d.Set(name, p); err != nil {
			return nil, fmt.Errorf("new dispatcher with auth and env: register %s: %w", name, err)
		}
	}
	return d, nil
}

// newDispatcherWithAuth registers the named providers against the auth
// store. A family is registered only when its credential resolves right
// now — the proxy must not advertise models it cannot serve.
func newDispatcherWithAuth(store credential.Store, names []string) (*Dispatcher, error) {
	if store == nil {
		return nil, fmt.Errorf("new dispatcher with auth: nil credential store")
	}
	d := NewDispatcher()
	for _, name := range names {
		p, err := authBackedProvider(context.Background(), store, name)
		if err != nil {
			// No usable credential for this family — skip silently.
			continue
		}
		if err := d.Set(name, p); err != nil {
			return nil, fmt.Errorf("new dispatcher with auth: register %s: %w", name, err)
		}
	}
	return d, nil
}

// authBackedProvider builds one adapter whose credential is re-resolved
// from the auth store on every call, after confirming the credential
// resolves at least once.
func authBackedProvider(ctx context.Context, store credential.Store, name string) (provider.Adapter, error) {
	source, err := credential.NewAutoSource(store, name)
	if err != nil {
		return nil, fmt.Errorf("auth-backed provider %s: %w", name, err)
	}
	decorator := source.Decorator()
	// Probe once so an absent credential keeps the family out of
	// /v1/models, matching the pre-migration semantics. The adapter
	// still re-resolves per call; this result is deliberately discarded.
	if _, err := decorator(ctx); err != nil {
		return nil, fmt.Errorf("auth-backed provider %s: %w", name, err)
	}
	p, err := provider.New(name, provider.Options{Decorator: decorator})
	if err != nil {
		return nil, fmt.Errorf("auth-backed provider %s: %w", name, err)
	}
	return p, nil
}
