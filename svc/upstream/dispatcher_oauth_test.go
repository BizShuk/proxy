package upstream

import (
	"context"
	"testing"
	"time"

	authmodel "github.com/bizshuk/auth/model"
	svc "github.com/bizshuk/auth/svc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore implements the credentialStore interface declared in
// credential.go for testing BuildProvider / NewDispatcherWithAuth
// without touching the real auth FileStore.
type stubStore struct {
	creds map[string]*authmodel.Credential
}

func (s *stubStore) Dir() string { return "" }
func (s *stubStore) Load(name string) (*authmodel.Credential, error) {
	c, ok := s.creds[name]
	if !ok {
		return nil, authmodel.ErrNotFound
	}
	return c, nil
}
func (s *stubStore) List() ([]*authmodel.Credential, error) {
	out := make([]*authmodel.Credential, 0, len(s.creds))
	for _, c := range s.creds {
		out = append(out, c)
	}
	return out, nil
}
func (s *stubStore) Save(c *authmodel.Credential) error {
	s.creds[c.Name()] = c
	return nil
}

// newStubResolver wraps a stubStore into a svc.Resolver. We use a
// nil authenticatorFor + nil envLookup since BuildProvider only
// reads the credential fields, not the upstream authenticators.
func newStubResolver(creds ...*authmodel.Credential) *svc.Resolver {
	store := &stubStore{creds: make(map[string]*authmodel.Credential)}
	for _, c := range creds {
		store.creds[c.Name()] = c
	}
	return svc.NewResolver(store, nil, nil)
}

func TestBuildProviderAPIKeyAnthropic(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "anthropic",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "sk-test",
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", p.ID())
}

func TestBuildProviderAPIKeyCodex(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "codex",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "sk-test",
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "codex", p.ID())
}

func TestBuildProviderOAuthAnthropic(t *testing.T) {
	cred := &authmodel.Credential{
		Provider:     "anthropic",
		Kind:         authmodel.KIND_OAUTH,
		AccessToken:  "sk-ant-oauth-test",
		RefreshToken: "refresh-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "anthropic", p.ID())
}

func TestBuildProviderOAuthCodexWithAccountID(t *testing.T) {
	cred := &authmodel.Credential{
		Provider:     "codex",
		Kind:         authmodel.KIND_OAUTH,
		AccessToken:  "oauth-codex",
		RefreshToken: "refresh-codex",
		ExpiresAt:    time.Now().Add(time.Hour),
		AccountID:    "chatgpt-acct-123",
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "codex", p.ID())
}

func TestBuildProviderOAuthAntigravityWithEmail(t *testing.T) {
	cred := &authmodel.Credential{
		Provider:     "antigravity",
		Kind:         authmodel.KIND_OAUTH,
		AccessToken:  "oauth-ag",
		RefreshToken: "refresh-ag",
		ExpiresAt:    time.Now().Add(time.Hour),
		Account:      "user@example.com",
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "antigravity", p.ID())
}

func TestBuildProviderOAuthGrok(t *testing.T) {
	cred := &authmodel.Credential{
		Provider:     "grok",
		Kind:         authmodel.KIND_OAUTH,
		AccessToken:  "oauth-grok",
		RefreshToken: "refresh-grok",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	p, err := BuildProvider(cred)
	require.NoError(t, err)
	assert.Equal(t, "grok", p.ID())
}

func TestBuildProviderRejectsUnknownFamily(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "mystery-model",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "sk-test",
	}
	_, err := BuildProvider(cred)
	assert.Error(t, err)
}

func TestBuildProviderRejectsEmptyAPIKey(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "anthropic",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "",
	}
	_, err := BuildProvider(cred)
	assert.Error(t, err)
}

func TestBuildProviderRejectsEmptyOAuthAccessToken(t *testing.T) {
	cred := &authmodel.Credential{
		Provider:    "anthropic",
		Kind:        authmodel.KIND_OAUTH,
		AccessToken: "",
	}
	_, err := BuildProvider(cred)
	assert.Error(t, err)
}

func TestBuildProviderRejectsNilCredential(t *testing.T) {
	_, err := BuildProvider(nil)
	assert.Error(t, err)
}

func TestBuildProviderRejectsBlankFamily(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "",
		Kind:     authmodel.KIND_API_KEY,
		APIKey:   "sk",
	}
	_, err := BuildProvider(cred)
	assert.Error(t, err)
}

func TestBuildProviderRejectsUnsupportedKind(t *testing.T) {
	cred := &authmodel.Credential{
		Provider: "anthropic",
		Kind:     "service-account-magic",
		APIKey:   "sk",
	}
	_, err := BuildProvider(cred)
	assert.Error(t, err)
}

func TestNewDispatcherWithAuthResolvesAllFamilies(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	resolver := newStubResolver(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: "sk-a"},
		&authmodel.Credential{Provider: "codex", Kind: authmodel.KIND_OAUTH, AccessToken: "tok-c", RefreshToken: "ref-c", ExpiresAt: expires},
		&authmodel.Credential{Provider: "grok", Kind: authmodel.KIND_OAUTH, AccessToken: "tok-g", RefreshToken: "ref-g", ExpiresAt: expires},
	)
	d, err := NewDispatcherWithAuth(resolver)
	require.NoError(t, err)

	ids := d.IDs()
	assert.ElementsMatch(t, []string{"anthropic", "codex", "grok"}, ids)
}

func TestNewDispatcherWithAuthSkipsFamiliesWithoutCredentials(t *testing.T) {
	// Only anthropic has a credential; others silently skipped.
	resolver := newStubResolver(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: "sk-a"},
	)
	d, err := NewDispatcherWithAuth(resolver)
	require.NoError(t, err)
	ids := d.IDs()
	assert.ElementsMatch(t, []string{"anthropic"}, ids)
}

func TestNewDispatcherWithAuthSkipsFamiliesWithMalformedCredentials(t *testing.T) {
	// anthropic has empty api_key → BuildProvider fails → skip.
	resolver := newStubResolver(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: ""},
		&authmodel.Credential{Provider: "codex", Kind: authmodel.KIND_OAUTH, AccessToken: "ok"},
		&authmodel.Credential{Provider: "grok", Kind: authmodel.KIND_API_KEY, APIKey: "ok"},
	)
	d, err := NewDispatcherWithAuth(resolver)
	require.NoError(t, err)
	ids := d.IDs()
	assert.ElementsMatch(t, []string{"codex", "grok"}, ids, "anthropic must be skipped (empty key)")
}

func TestNewDispatcherWithAuthRejectsNilResolver(t *testing.T) {
	_, err := NewDispatcherWithAuth(nil)
	assert.Error(t, err)
}

func TestNewDispatcherWithAuthAndEnvFallsBack(t *testing.T) {
	// Set one auth credential; let env-fill the rest.
	resolver := newStubResolver(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: "sk-from-auth"},
	)
	// Anthropic env vars must be blank so the env path also fails —
	// we want only the auth credential to register.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("ANTIGRAVITY_API_KEY", "")

	d, err := NewDispatcherWithAuthAndEnv(resolver)
	require.NoError(t, err)
	// anthropic from auth + ollama from env (keyless).
	assert.ElementsMatch(t, []string{"anthropic", "ollama"}, d.IDs())
}

func TestNewDispatcherWithAuthAndEnvPrefersAuth(t *testing.T) {
	// anthropic has BOTH auth cred AND env var. The auth path runs
	// first; the env fallback must NOT overwrite it.
	resolver := newStubResolver(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: "sk-from-auth"},
	)
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-should-not-win")

	d, err := NewDispatcherWithAuthAndEnv(resolver)
	require.NoError(t, err)
	// Just confirm anthropic is present — the actual key winner is
	// not observable from outside the dispatcher without instrumentation,
	// but we cover that the dispatch succeeds and the family is in.
	_, ok := d.Lookup("anthropic")
	assert.True(t, ok)
}

func TestCredentialResolverAsInnerReturnsResolver(t *testing.T) {
	store := &stubStore{creds: map[string]*authmodel.Credential{
		"anthropic-oauth": {Provider: "anthropic", Kind: authmodel.KIND_OAUTH, AccessToken: "tok"},
	}}
	r := NewCredentialResolver(store, nil, func(string) (string, bool) { return "", false })
	inner := r.AsInner()
	require.NotNil(t, inner)

	ctx := context.Background()
	cred, err := inner.Resolve(ctx, "anthropic")
	require.NoError(t, err)
	assert.Equal(t, "anthropic", cred.Provider)
	assert.Equal(t, authmodel.KIND_OAUTH, cred.Kind)
	assert.Equal(t, "tok", cred.AccessToken)
}

func TestCredentialResolverAsInnerNilSafe(t *testing.T) {
	var r *CredentialResolver
	assert.Nil(t, r.AsInner())
}