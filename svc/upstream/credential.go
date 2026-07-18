package upstream

import (
	"context"
	"errors"

	authmodel "github.com/bizshuk/auth/model"
	svc "github.com/bizshuk/auth/svc"
	"github.com/bizshuk/proxy/model"
)

type credentialStore interface {
	Dir() string
	Load(string) (*authmodel.Credential, error)
	List() ([]*authmodel.Credential, error)
	Save(*authmodel.Credential) error
}

type authenticatorResolver func(*authmodel.Credential) (authmodel.Authenticator, error)
type environmentLookup func(string) (string, bool)

// ResolvedCredential is the validated credential selected for one request.
type ResolvedCredential = authmodel.Credential

// CredentialResolver adapts the shared svc.Resolver onto the proxy error
// surface. Selection, expiry refresh, and persistence live in auth; this
// wrapper only maps failures to credential_unavailable proxy errors.
type CredentialResolver struct {
	inner *svc.Resolver
}

// NewCredentialResolver constructs a request-scoped credential resolver.
func NewCredentialResolver(store credentialStore, resolveAuth authenticatorResolver, lookupEnvironment environmentLookup) *CredentialResolver {
	return &CredentialResolver{
		inner: svc.NewResolver(store, svc.AuthenticatorFor(resolveAuth), svc.EnvLookup(lookupEnvironment)),
	}
}

// Resolve selects a provider credential and refreshes it when needed.
func (r *CredentialResolver) Resolve(ctx context.Context, providerFamily string) (*ResolvedCredential, error) {
	var inner *svc.Resolver
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
	proxyErr := &model.ProxyError{
		Kind:    model.ERROR_UNAVAILABLE,
		Code:    "credential_unavailable",
		Message: "credential resolution failed",
		Cause:   err,
	}
	var unavailableErr *svc.UnavailableError
	if errors.As(err, &unavailableErr) {
		proxyErr.Message = unavailableErr.Message
		proxyErr.Cause = unavailableErr.Cause
	}
	return proxyErr
}
