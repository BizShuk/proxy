package upstream

import (
	"context"
	"errors"

	"github.com/bizshuk/agentsdk/auth/auth"
	"github.com/bizshuk/proxy/protocol"
)

type credentialStore interface {
	Dir() string
	Load(string) (*auth.Credential, error)
	List() ([]*auth.Credential, error)
	Save(*auth.Credential) error
}

type authenticatorResolver func(*auth.Credential) (auth.Authenticator, error)
type environmentLookup func(string) (string, bool)

// ResolvedCredential is the validated credential selected for one request.
type ResolvedCredential = auth.Credential

// CredentialResolver adapts the shared auth.Resolver onto the proxy error
// surface. Selection, expiry refresh, and persistence live in auth; this
// wrapper only maps failures to credential_unavailable proxy errors.
type CredentialResolver struct {
	inner *auth.Resolver
}

// NewCredentialResolver constructs a request-scoped credential resolver.
func NewCredentialResolver(store credentialStore, resolveAuth authenticatorResolver, lookupEnvironment environmentLookup) *CredentialResolver {
	return &CredentialResolver{
		inner: auth.NewResolver(store, auth.AuthenticatorFor(resolveAuth), auth.EnvLookup(lookupEnvironment)),
	}
}

// Resolve selects a provider credential and refreshes it when needed.
func (r *CredentialResolver) Resolve(ctx context.Context, providerFamily string) (*ResolvedCredential, error) {
	var inner *auth.Resolver
	if r != nil {
		inner = r.inner
	}
	cred, err := inner.Resolve(ctx, providerFamily)
	if err != nil {
		return nil, asCredentialUnavailable(err)
	}
	return cred, nil
}

func asCredentialUnavailable(err error) error {
	proxyErr := &protocol.ProxyError{
		Kind:    protocol.ERROR_UNAVAILABLE,
		Code:    "credential_unavailable",
		Message: "credential resolution failed",
		Cause:   err,
	}
	var unavailableErr *auth.UnavailableError
	if errors.As(err, &unavailableErr) {
		proxyErr.Message = unavailableErr.Message
		proxyErr.Cause = unavailableErr.Cause
	}
	return proxyErr
}
