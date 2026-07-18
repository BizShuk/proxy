package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bizshuk/agentsdk/auth/model"
	"github.com/bizshuk/proxy/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCredentialResolverRefreshesWithRequestContextAndSaves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := newFakeCredentialStore(t, []*model.Credential{{
		Provider: "openai", Kind: model.KIND_OAUTH, AccessToken: "old", RefreshToken: "refresh",
		ExpiresAt: time.Now().Add(-time.Minute),
	}})
	authenticator := &fakeAuthenticator{refresh: &model.Credential{
		Provider: "openai", Kind: model.KIND_OAUTH, AccessToken: "new", RefreshToken: "rotated",
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	resolver := NewCredentialResolver(store, func(*model.Credential) (model.Authenticator, error) {
		return authenticator, nil
	}, func(string) (string, bool) { return "", false })

	cred, err := resolver.Resolve(ctx, "openai")
	require.NoError(t, err)
	assert.Equal(t, "new", cred.AccessToken)
	assert.Same(t, ctx, authenticator.refreshContext)
	require.Len(t, store.saved, 1)
	assert.Equal(t, "rotated", store.saved[0].RefreshToken)
}

func TestCredentialResolverSelectsActiveThenAlphabeticFallback(t *testing.T) {
	creds := []*model.Credential{
		{Provider: "openai", Kind: model.KIND_API_KEY, Account: "zeta", APIKey: "zeta-key"},
		{Provider: "anthropic", Kind: model.KIND_API_KEY, Account: "other", APIKey: "other-key"},
		{Provider: "openai", Kind: model.KIND_API_KEY, Account: "alpha", APIKey: "alpha-key"},
	}

	t.Run("active selection", func(t *testing.T) {
		store := newFakeCredentialStore(t, creds)
		writeActiveMap(t, store.Dir(), map[string]string{"openai": creds[0].Name()})
		resolver := newTestCredentialResolver(store)

		cred, err := resolver.Resolve(context.Background(), "openai")
		require.NoError(t, err)
		assert.Equal(t, "zeta-key", cred.APIKey)
	})

	t.Run("alphabetic fallback", func(t *testing.T) {
		store := newFakeCredentialStore(t, creds)
		resolver := newTestCredentialResolver(store)

		cred, err := resolver.Resolve(context.Background(), "openai")
		require.NoError(t, err)
		assert.Equal(t, "alpha-key", cred.APIKey)
	})
}

func TestCredentialResolverUsesProviderEnvironmentFallback(t *testing.T) {
	tests := []struct {
		provider string
		envName  string
	}{
		{provider: "anthropic", envName: "ANTHROPIC_API_KEY"},
		{provider: "openai", envName: "OPENAI_API_KEY"},
		{provider: "xai", envName: "XAI_API_KEY"},
		{provider: "minimax", envName: "MINIMAX_API_KEY"},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			store := newFakeCredentialStore(t, nil)
			resolver := NewCredentialResolver(store, nil, func(name string) (string, bool) {
				if name == tc.envName {
					return tc.provider + "-secret", true
				}
				return "", false
			})

			cred, err := resolver.Resolve(context.Background(), tc.provider)
			require.NoError(t, err)
			assert.Equal(t, tc.provider, cred.Provider)
			assert.Equal(t, model.KIND_API_KEY, cred.Kind)
			assert.Equal(t, tc.provider+"-secret", cred.APIKey)
		})
	}
}

func TestCredentialResolverRejectsInvalidActiveSelection(t *testing.T) {
	tests := []struct {
		name      string
		activeRaw []byte
		creds     []*model.Credential
	}{
		{
			name:      "malformed active map",
			activeRaw: []byte(`{"openai":`),
		},
		{
			name:      "active credential cannot be loaded",
			activeRaw: []byte(`{"openai":"missing"}`),
		},
		{
			name:      "active credential belongs to another provider",
			activeRaw: []byte(`{"openai":"anthropic-apikey"}`),
			creds: []*model.Credential{{
				Provider: "anthropic", Kind: model.KIND_API_KEY, APIKey: "secret",
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeCredentialStore(t, tc.creds)
			require.NoError(t, os.WriteFile(filepath.Join(store.Dir(), "active.json"), tc.activeRaw, 0o600))
			resolver := newTestCredentialResolver(store)

			_, err := resolver.Resolve(context.Background(), "openai")
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, protocol.ERROR_UNAVAILABLE, proxyErr.Kind)
		})
	}
}

func TestCredentialResolverReturnsUnavailableOnRefreshOrSaveFailure(t *testing.T) {
	refreshErr := errors.New("refresh failed")
	saveErr := errors.New("save failed")
	tests := []struct {
		name          string
		authenticator *fakeAuthenticator
		saveErr       error
	}{
		{
			name:          "refresh failure",
			authenticator: &fakeAuthenticator{refreshErr: refreshErr},
		},
		{
			name: "save failure",
			authenticator: &fakeAuthenticator{refresh: &model.Credential{
				Provider: "openai", Kind: model.KIND_OAUTH, AccessToken: "rotated-access", RefreshToken: "rotated-refresh",
				ExpiresAt: time.Now().Add(time.Hour),
			}},
			saveErr: saveErr,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeCredentialStore(t, []*model.Credential{{
				Provider: "openai", Kind: model.KIND_OAUTH, AccessToken: "expired", RefreshToken: "refresh",
				ExpiresAt: time.Now().Add(-time.Minute),
			}})
			store.saveErr = tc.saveErr
			resolver := NewCredentialResolver(store, func(*model.Credential) (model.Authenticator, error) {
				return tc.authenticator, nil
			}, func(string) (string, bool) { return "", false })

			cred, err := resolver.Resolve(context.Background(), "openai")
			assert.Nil(t, cred)
			var proxyErr *protocol.ProxyError
			require.ErrorAs(t, err, &proxyErr)
			assert.Equal(t, protocol.ERROR_UNAVAILABLE, proxyErr.Kind)
			assert.NotEqual(t, "rotated-access", valueOrEmpty(cred))
		})
	}
}

func TestCredentialResolverRejectsMissingCredential(t *testing.T) {
	store := newFakeCredentialStore(t, nil)
	resolver := newTestCredentialResolver(store)

	_, err := resolver.Resolve(context.Background(), "openai")
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNAVAILABLE, proxyErr.Kind)
	assert.Equal(t, 503, proxyErr.StatusCode())
}

type fakeCredentialStore struct {
	dir     string
	creds   []*model.Credential
	saved   []*model.Credential
	listErr error
	loadErr error
	saveErr error
}

func newFakeCredentialStore(t *testing.T, creds []*model.Credential) *fakeCredentialStore {
	t.Helper()
	return &fakeCredentialStore{dir: t.TempDir(), creds: creds}
}

func (s *fakeCredentialStore) Dir() string { return s.dir }

func (s *fakeCredentialStore) Load(name string) (*model.Credential, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	for _, cred := range s.creds {
		if cred.Name() == name {
			copy := *cred
			return &copy, nil
		}
	}
	return nil, model.ErrNotFound
}

func (s *fakeCredentialStore) List() ([]*model.Credential, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]*model.Credential(nil), s.creds...), nil
}

func (s *fakeCredentialStore) Save(cred *model.Credential) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	copy := *cred
	s.saved = append(s.saved, &copy)
	return nil
}

type fakeAuthenticator struct {
	refresh        *model.Credential
	refreshErr     error
	refreshContext context.Context
}

func (a *fakeAuthenticator) Provider() string { return "openai" }
func (a *fakeAuthenticator) Kind() model.Kind  { return model.KIND_OAUTH }

func (a *fakeAuthenticator) Login(context.Context) (*model.Credential, error) {
	return nil, errors.New("unexpected login")
}

func (a *fakeAuthenticator) Refresh(ctx context.Context, _ *model.Credential) (*model.Credential, error) {
	a.refreshContext = ctx
	return a.refresh, a.refreshErr
}

func (a *fakeAuthenticator) Verify(context.Context, *model.Credential) (*model.VerifyResult, error) {
	return nil, errors.New("unexpected verify")
}

func newTestCredentialResolver(store credentialStore) *CredentialResolver {
	return NewCredentialResolver(store, func(*model.Credential) (model.Authenticator, error) {
		return nil, errors.New("unexpected authenticator resolution")
	}, func(string) (string, bool) { return "", false })
}

func writeActiveMap(t *testing.T, dir string, active map[string]string) {
	t.Helper()
	raw, err := json.Marshal(active)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "active.json"), raw, 0o600))
}

func valueOrEmpty(cred *ResolvedCredential) string {
	if cred == nil {
		return ""
	}
	return cred.AccessToken
}
