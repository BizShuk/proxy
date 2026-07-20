// Package upstream — dispatcher_oauth.go
//
// BuildProvider is the seam between authmodel.Credential (the auth
// submodule's persisted credential shape) and the live core.Provider
// from each provider package. It is the only place in the proxy that
// knows how to translate the auth storage shape into the per-provider
// OAuth credential struct.
//
// Per-provider routing table:
//
//	family       api_key                       oauth
//	anthropic    anthropic.New(WithAPIKey)     anthropic.NewWithOAuth
//	codex        codex.New(WithAPIKey)         codex.NewWithOAuth
//	antigravity  antigravity.New(WithAPIKey)   antigravity.NewWithOAuth
//	grok         grok.New(WithAPIKey)          grok.NewWithOAuth
//	ollama       ollama.New(WithAPIKey)        (keyless only)
//	minimax      minimax.New(WithAPIKey)       (api key only)
//
// Adding a new provider: add a case in buildAPIKeyProvider / buildOAuthProvider
// and a row above. The Dispatcher itself is provider-agnostic; this
// file is the only place that needs to know the per-provider shape.
package upstream

import (
	"context"
	"fmt"
	"time"

	"github.com/bizshuk/agentsdk/core"
	authmodel "github.com/bizshuk/auth/model"
	svc "github.com/bizshuk/auth/svc"

	"github.com/bizshuk/agentsdk/provider/anthropic"
	"github.com/bizshuk/agentsdk/provider/antigravity"
	"github.com/bizshuk/agentsdk/provider/codex"
	"github.com/bizshuk/agentsdk/provider/google"
	"github.com/bizshuk/agentsdk/provider/grok"
	"github.com/bizshuk/agentsdk/provider/minimax"
	"github.com/bizshuk/agentsdk/provider/ollama"
)

// BuildProvider resolves a persisted auth credential into a live
// core.Provider. Returns an error when the credential's family has no
// constructor route or the credential is malformed.
//
// The returned provider is bound to the credential at construction
// time. OAuth providers take a snapshot of the access token; when the
// token rotates (out-of-band refresh), call Dispatcher.Replace with
// the rebuilt provider to swap in the new credential.
func BuildProvider(cred *authmodel.Credential) (core.Provider, error) {
	if cred == nil {
		return nil, fmt.Errorf("build provider: nil credential")
	}
	if cred.Provider == "" {
		return nil, fmt.Errorf("build provider: credential has no provider family")
	}
	switch cred.Kind {
	case authmodel.KIND_API_KEY:
		return buildAPIKeyProvider(cred)
	case authmodel.KIND_OAUTH:
		return buildOAuthProvider(cred)
	default:
		return nil, fmt.Errorf("build provider: unsupported credential kind %q for family %q", cred.Kind, cred.Provider)
	}
}

func buildAPIKeyProvider(cred *authmodel.Credential) (core.Provider, error) {
	if cred.APIKey == "" {
		return nil, fmt.Errorf("build provider: api_key credential for %q has empty key", cred.Provider)
	}
	switch cred.Provider {
	case "anthropic":
		return anthropic.New(anthropic.WithAPIKey(cred.APIKey))
	case "codex":
		return codex.New(codex.WithAPIKey(cred.APIKey))
	case "grok":
		return grok.New(grok.WithAPIKey(cred.APIKey))
	case "antigravity":
		return antigravity.New(antigravity.WithAPIKey(cred.APIKey))
	case "ollama":
		// Ollama is keyless; pass empty API key for unprotected local
		// servers. For protected servers (LM Studio / vLLM with auth),
		// the credential carries the bearer.
		return ollama.New(ollama.WithAPIKey(cred.APIKey))
	case "minimax":
		return minimax.New(minimax.WithAPIKey(cred.APIKey))
	case "google":
		return google.New(google.WithAPIKey(cred.APIKey))
	default:
		return nil, fmt.Errorf("build provider: no api_key path for family %q", cred.Provider)
	}
}

func buildOAuthProvider(cred *authmodel.Credential) (core.Provider, error) {
	if cred.AccessToken == "" {
		return nil, fmt.Errorf("build provider: oauth credential for %q has empty access_token", cred.Provider)
	}
	oauthCreds := authmodelToOAuth(cred)
	switch cred.Provider {
	case "anthropic":
		return anthropic.NewWithOAuth(anthropic.OAuthCredentials{
			AccessToken:  oauthCreds.AccessToken,
			RefreshToken: oauthCreds.RefreshToken,
			ExpiresAt:    oauthCreds.ExpiresAt,
		})
	case "codex":
		return codex.NewWithOAuth(codex.OAuthCredentials{
			AccessToken:  oauthCreds.AccessToken,
			RefreshToken: oauthCreds.RefreshToken,
			ExpiresAt:    oauthCreds.ExpiresAt,
			AccountID:    oauthCreds.AccountID,
		})
	case "antigravity":
		return antigravity.NewWithOAuth(antigravity.OAuthCredentials{
			AccessToken:  oauthCreds.AccessToken,
			RefreshToken: oauthCreds.RefreshToken,
			ExpiresAt:    oauthCreds.ExpiresAt,
			Email:        oauthCreds.Email,
		})
	case "grok":
		return grok.NewWithOAuth(grok.OAuthCredentials{
			AccessToken:  oauthCreds.AccessToken,
			RefreshToken: oauthCreds.RefreshToken,
			ExpiresAt:    oauthCreds.ExpiresAt,
		})
	default:
		return nil, fmt.Errorf("build provider: no oauth path for family %q", cred.Provider)
	}
}

// authmodelToOAuth is the field-by-field adapter between the auth
// submodule's Credential and the provider package's OAuthCredentials
// shape. Centralized here so changes to the wire shape land in one
// file, not 4.
func authmodelToOAuth(cred *authmodel.Credential) struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	AccountID    string
	Email        string
} {
	return struct {
		AccessToken  string
		RefreshToken string
		ExpiresAt    time.Time
		AccountID    string
		Email        string
	}{
		AccessToken:  cred.AccessToken,
		RefreshToken: cred.RefreshToken,
		ExpiresAt:    cred.ExpiresAt,
		AccountID:    cred.AccountID,
		Email:        cred.Account,
	}
}

// NewDispatcherWithAuth builds a Dispatcher by resolving credentials
// from the auth store for every known family. Families whose
// credentials are absent (no env, no auth.json entry) are silently
// skipped — the dispatcher still works for the families that do
// resolve.
//
// This is the production wiring path: `auth login --provider X` saves
// credentials; the proxy picks them up at startup. Token rotation
// requires re-calling BuildProvider and Dispatcher.Replace.
func NewDispatcherWithAuth(resolver *svc.Resolver) (*Dispatcher, error) {
	return newDispatcherWithAuth(resolver, familiesInDefaultOrder())
}

// NewDispatcherWithAuthAndEnv is a fallback path: it tries the auth
// store first; for families with no credential, it falls back to
// env-var lookup via provider.New(). Useful during dev / when only
// some providers have completed OAuth login.
//
// Note: env-var fallback always builds api_key-style providers —
// there is no env var for OAuth tokens.
func NewDispatcherWithAuthAndEnv(resolver *svc.Resolver) (*Dispatcher, error) {
	d, err := newDispatcherWithAuth(resolver, familiesInDefaultOrder())
	if err != nil {
		return nil, err
	}
	// Augment with env-var fallbacks for any family missing from auth.
	if _, ok := d.Lookup("anthropic"); !ok {
		if p, err := anthropic.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("ollama"); !ok {
		if p, err := ollama.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("grok"); !ok {
		if p, err := grok.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("antigravity"); !ok {
		if p, err := antigravity.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("codex"); !ok {
		if p, err := codex.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("minimax"); !ok {
		if p, err := minimax.New(); err == nil {
			_ = d.Set(p)
		}
	}
	if _, ok := d.Lookup("google"); !ok {
		if p, err := google.New(); err == nil {
			_ = d.Set(p)
		}
	}
	return d, nil
}

func familiesInDefaultOrder() []string {
	return []string{"anthropic", "codex", "antigravity", "grok", "ollama", "minimax", "google"}
}

func newDispatcherWithAuth(resolver *svc.Resolver, families []string) (*Dispatcher, error) {
	if resolver == nil {
		return nil, fmt.Errorf("new dispatcher with auth: nil resolver")
	}
	d := NewDispatcher()
	for _, family := range families {
		cred, err := resolver.Resolve(context.Background(), family)
		if err != nil {
			// No credential for this family — skip silently. The
			// dispatcher stays usable for other families.
			continue
		}
		p, err := BuildProvider(cred)
		if err != nil {
			// Malformed credential — log and skip rather than fail
			// the entire dispatcher.
			continue
		}
		if err := d.Set(p); err != nil {
			return nil, fmt.Errorf("new dispatcher with auth: register %s: %w", family, err)
		}
	}
	return d, nil
}
