package upstream

import (
	"sort"
	"testing"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStore implements credential.Store (== svc.ResolverStore, the same
// shape as the credentialStore interface in credential.go) so the
// dispatcher wiring can be exercised without the real auth FileStore.
type stubStore struct {
	creds map[string]*authmodel.Credential
}

func (s *stubStore) Read(name string) (*authmodel.Credential, error) {
	c, ok := s.creds[name]
	if !ok {
		return nil, authmodel.ErrNotFound
	}
	return c, nil
}

// List mirrors gosdk/file.Store: sorted names, not credentials.
func (s *stubStore) List() ([]string, error) {
	out := make([]string, 0, len(s.creds))
	for name := range s.creds {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func (s *stubStore) Write(name string, c *authmodel.Credential) error {
	s.creds[name] = c
	return nil
}

func newStubStore(creds ...*authmodel.Credential) *stubStore {
	store := &stubStore{creds: make(map[string]*authmodel.Credential)}
	for _, c := range creds {
		store.creds[c.Name()] = c
	}
	return store
}

// blankProviderEnv clears every credential env var the registered
// adapters read, so a test observes only the auth-store path. Ollama is
// keyless and always constructs; it is the one family the env fallback
// can still contribute.
func blankProviderEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN",
		"XAI_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY",
		"MINIMAX_API_KEY", "ANTIGRAVITY_API_KEY",
	} {
		t.Setenv(key, "")
	}
}

// Credentials are keyed by the AUTH family, which is not always the
// agentsdk registry name — agentsdk owns that mapping in
// credential.RouteID (codex → openai, grok → xai). These helpers make
// the two-name reality explicit at the call site.
func anthropicAPIKey() *authmodel.Credential {
	return &authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: "sk-a"}
}
func codexOAuth() *authmodel.Credential {
	return &authmodel.Credential{Provider: "openai", Kind: authmodel.KIND_OAUTH, AccessToken: "tok-c"}
}
func grokAPIKey() *authmodel.Credential {
	return &authmodel.Credential{Provider: "xai", Kind: authmodel.KIND_API_KEY, APIKey: "sk-x"}
}

func TestNewDispatcherWithAuthRegistersUnderTheAgentsdkName(t *testing.T) {
	blankProviderEnv(t)
	// Stored under the AUTH families anthropic / openai / xai; the
	// dispatcher must key them by the AGENTSDK names anthropic / codex /
	// grok. That translation is credential.RouteID's job, not the
	// proxy's — this test pins that we consume it correctly.
	store := newStubStore(anthropicAPIKey(), codexOAuth(), grokAPIKey())

	d, err := NewDispatcherWithAuth(store)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anthropic", "codex", "grok"}, d.IDs())
}

func TestNewDispatcherWithAuthSkipsFamiliesWithoutCredentials(t *testing.T) {
	blankProviderEnv(t)
	store := newStubStore(anthropicAPIKey())

	d, err := NewDispatcherWithAuth(store)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anthropic"}, d.IDs())
}

func TestNewDispatcherWithAuthSkipsFamiliesWithMalformedCredentials(t *testing.T) {
	blankProviderEnv(t)
	// anthropic carries an empty api_key → the credential fails
	// validation inside the auth resolver → the family is skipped
	// rather than failing the whole dispatcher.
	store := newStubStore(
		&authmodel.Credential{Provider: "anthropic", Kind: authmodel.KIND_API_KEY, APIKey: ""},
		codexOAuth(),
		grokAPIKey(),
	)

	d, err := NewDispatcherWithAuth(store)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"codex", "grok"}, d.IDs(),
		"anthropic must be skipped (empty key)")
}

func TestNewDispatcherWithAuthRejectsNilStore(t *testing.T) {
	_, err := NewDispatcherWithAuth(nil)
	assert.Error(t, err)
}

func TestNewDispatcherWithAuthAndEnvFallsBack(t *testing.T) {
	blankProviderEnv(t)
	store := newStubStore(anthropicAPIKey())

	d, err := NewDispatcherWithAuthAndEnv(store)
	require.NoError(t, err)
	// anthropic from the auth store + ollama from env (keyless).
	assert.ElementsMatch(t, []string{"anthropic", "ollama"}, d.IDs())
}

func TestNewDispatcherWithAuthAndEnvPrefersAuth(t *testing.T) {
	blankProviderEnv(t)
	// anthropic has BOTH an auth credential AND an env var. The auth
	// path runs first and Set refuses duplicates, so the env fallback
	// must not overwrite it.
	store := newStubStore(anthropicAPIKey())
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-should-not-win")

	d, err := NewDispatcherWithAuthAndEnv(store)
	require.NoError(t, err)
	_, ok := d.Lookup("anthropic")
	assert.True(t, ok)
}

// The dispatcher advertises models from the agentsdk registry, not from
// the adapter — so a family registered from the auth store contributes
// its bundled catalog without any further network or credential I/O.
func TestAuthBackedFamiliesAdvertiseTheRegistryCatalog(t *testing.T) {
	blankProviderEnv(t)
	store := newStubStore(anthropicAPIKey())

	d, err := NewDispatcherWithAuth(store)
	require.NoError(t, err)
	assert.NotEmpty(t, d.AdvertisedModels(),
		"anthropic's bundled catalog must reach /v1/models")
}
