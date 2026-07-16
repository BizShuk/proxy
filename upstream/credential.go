package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bizshuk/agentsdk/auth"
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

// CredentialResolver selects, refreshes, and persists provider credentials.
type CredentialResolver struct {
	store             credentialStore
	resolveAuth       authenticatorResolver
	lookupEnvironment environmentLookup
}

// NewCredentialResolver constructs a request-scoped credential resolver.
func NewCredentialResolver(store credentialStore, resolveAuth authenticatorResolver, lookupEnvironment environmentLookup) *CredentialResolver {
	return &CredentialResolver{
		store:             store,
		resolveAuth:       resolveAuth,
		lookupEnvironment: lookupEnvironment,
	}
}

// Resolve selects a provider credential and refreshes it when needed.
func (r *CredentialResolver) Resolve(ctx context.Context, providerFamily string) (*ResolvedCredential, error) {
	providerFamily = strings.ToLower(strings.TrimSpace(providerFamily))
	if providerFamily == "" {
		return nil, unavailableCredentialError("credential provider is blank", nil)
	}
	if r == nil || r.store == nil {
		return nil, unavailableCredentialError("credential store is unavailable", nil)
	}

	cred, err := r.resolveStored(providerFamily)
	if err != nil {
		return nil, err
	}
	if cred == nil {
		cred = r.resolveEnvironment(providerFamily)
	}
	if cred == nil {
		return nil, unavailableCredentialError(fmt.Sprintf("no credential available for provider %q", providerFamily), nil)
	}
	if err := cred.Validate(); err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("invalid credential for provider %q", providerFamily), err)
	}
	if !strings.EqualFold(cred.Provider, providerFamily) {
		return nil, unavailableCredentialError(fmt.Sprintf("credential provider %q does not match %q", cred.Provider, providerFamily), nil)
	}
	if !cred.Expired(auth.DEFAULT_EXPIRY_SKEW) {
		return cred, nil
	}
	return r.refresh(ctx, providerFamily, cred)
}

func (r *CredentialResolver) resolveStored(providerFamily string) (*auth.Credential, error) {
	active, err := r.loadActiveMap()
	if err != nil {
		return nil, err
	}
	if name, ok := active[providerFamily]; ok {
		cred, err := r.store.Load(name)
		if err != nil {
			return nil, unavailableCredentialError(fmt.Sprintf("load active credential for provider %q", providerFamily), err)
		}
		if !strings.EqualFold(cred.Provider, providerFamily) {
			return nil, unavailableCredentialError(fmt.Sprintf("active credential provider %q does not match %q", cred.Provider, providerFamily), nil)
		}
		return cred, nil
	}

	creds, err := r.store.List()
	if err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("list credentials for provider %q", providerFamily), err)
	}
	matching := make([]*auth.Credential, 0, len(creds))
	for _, cred := range creds {
		if cred != nil && strings.EqualFold(cred.Provider, providerFamily) {
			matching = append(matching, cred)
		}
	}
	slices.SortFunc(matching, func(left, right *auth.Credential) int {
		return strings.Compare(left.Name(), right.Name())
	})
	if len(matching) == 0 {
		return nil, nil
	}
	return matching[0], nil
}

func (r *CredentialResolver) loadActiveMap() (map[string]string, error) {
	path := filepath.Join(r.store.Dir(), "active.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, unavailableCredentialError("read active credential selection", err)
	}
	active := make(map[string]string)
	if err := json.Unmarshal(raw, &active); err != nil {
		return nil, unavailableCredentialError("parse active credential selection", err)
	}
	return active, nil
}

func (r *CredentialResolver) resolveEnvironment(providerFamily string) *auth.Credential {
	if r.lookupEnvironment == nil {
		return nil
	}
	environmentNames := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"xai":       "XAI_API_KEY",
		"minimax":   "MINIMAX_API_KEY",
	}
	name, ok := environmentNames[providerFamily]
	if !ok {
		return nil
	}
	value, ok := r.lookupEnvironment(name)
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	return &auth.Credential{
		Provider: providerFamily,
		Kind:     auth.KIND_API_KEY,
		APIKey:   value,
	}
}

func (r *CredentialResolver) refresh(ctx context.Context, providerFamily string, cred *auth.Credential) (*ResolvedCredential, error) {
	if r.resolveAuth == nil {
		return nil, unavailableCredentialError(fmt.Sprintf("refresh credential for provider %q", providerFamily), nil)
	}
	authenticator, err := r.resolveAuth(cred)
	if err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("resolve authenticator for provider %q", providerFamily), err)
	}
	refreshed, err := authenticator.Refresh(ctx, cred)
	if err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("refresh credential for provider %q", providerFamily), err)
	}
	if refreshed == nil {
		return nil, unavailableCredentialError(fmt.Sprintf("refresh credential for provider %q returned nil", providerFamily), nil)
	}
	if err := refreshed.Validate(); err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("refresh credential for provider %q returned invalid credential", providerFamily), err)
	}
	if !strings.EqualFold(refreshed.Provider, providerFamily) {
		return nil, unavailableCredentialError(fmt.Sprintf("refreshed credential provider %q does not match %q", refreshed.Provider, providerFamily), nil)
	}
	if err := r.store.Save(refreshed); err != nil {
		return nil, unavailableCredentialError(fmt.Sprintf("save refreshed credential for provider %q", providerFamily), err)
	}
	return refreshed, nil
}

func unavailableCredentialError(message string, cause error) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNAVAILABLE,
		Code:    "credential_unavailable",
		Message: message,
		Cause:   cause,
	}
}
