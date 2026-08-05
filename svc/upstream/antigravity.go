package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	agcore "github.com/bizshuk/agentsdk/core"
	"github.com/bizshuk/agentsdk/provider"
	ag "github.com/bizshuk/agentsdk/provider/antigravity"
	authmodel "github.com/bizshuk/auth/model"
)

// ANTIGRAVITY_PROFILE_ID names the Antigravity gateway profile. Everything
// else about the protocol — endpoints, client identity, host fallback, project
// discovery, the envelope and the schema dialect — belongs to
// agentsdk/provider/antigravity and is referenced there directly.
const ANTIGRAVITY_PROFILE_ID = "antigravity"

// antigravityProjects keeps one agentsdk client per credential so its own
// project cache survives between requests.
//
// agentsdk discovers the Cloud Code project once per Provider and remembers it.
// The proxy resolves a credential per request and builds no long-lived
// provider, so without this the discovery round-trip would run on every call.
// The map is the whole addition; the lookup itself stays agentsdk's.
type antigravityProjects struct {
	// baseURL pins the gateway host. Empty means agentsdk's own daily-then-
	// production order, which is what production wants; tests set it.
	baseURL string
	mu      sync.Mutex
	clients map[string]*ag.Provider
}

func newAntigravityProjects() *antigravityProjects {
	return &antigravityProjects{clients: make(map[string]*ag.Provider)}
}

// Resolve returns the Cloud Code project the credential bills against.
func (r *antigravityProjects) Resolve(ctx context.Context, cred *authmodel.Credential) (string, error) {
	if cred == nil {
		return "", authProxyError("antigravity credential is nil", nil)
	}
	// A credential that already carries its project needs no client at all.
	if project := strings.TrimSpace(cred.ProjectID); project != "" {
		return project, nil
	}
	if r == nil {
		return "", unavailableUpstreamError("antigravity project resolver is unavailable", nil)
	}
	client, err := r.clientFor(cred)
	if err != nil {
		return "", err
	}
	// agentsdk never fails this: an account with no provisioned project falls
	// back to the sentinel the reference clients use.
	return client.ProjectID(ctx, agcore.Auth{})
}

func (r *antigravityProjects) clientFor(cred *authmodel.Credential) (*ag.Provider, error) {
	token := strings.TrimSpace(cred.AccessToken)
	if token == "" {
		return nil, authProxyError("antigravity credential has no access token", nil)
	}

	key := cred.Name()
	r.mu.Lock()
	defer r.mu.Unlock()
	if client, found := r.clients[key]; found {
		return client, nil
	}

	config := provider.ResolvedConfig{Auth: agcore.Auth{Bearer: token}}
	// A credential pinned to a gateway must have its project looked up through
	// that same gateway, matching how the generation request is routed.
	baseURL := strings.TrimSpace(cred.BaseURL)
	if baseURL == "" {
		baseURL = r.baseURL
	}
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	client, err := ag.New(config)
	if err != nil {
		return nil, unavailableUpstreamError("build antigravity client", err)
	}
	r.clients[key] = client
	return client, nil
}

// injectAntigravityProject stamps the resolved project onto the request
// envelope. The transform cannot do this: the project comes from the
// credential, which only the transport layer holds.
func injectAntigravityProject(body []byte, project string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidRequestError("normalize antigravity request", err)
	}
	encoded, err := json.Marshal(project)
	if err != nil {
		return nil, fmt.Errorf("encode antigravity project: %w", err)
	}
	envelope["project"] = encoded
	return json.Marshal(envelope)
}
