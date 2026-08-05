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
	"github.com/bizshuk/proxy/model"
)

// The Antigravity protocol — endpoints, client identity, host fallback and
// project discovery — is owned by agentsdk/provider/antigravity. This file
// binds that contract onto the proxy's Profile/Client machinery; the constants
// below are aliases, not a second source of truth.
const (
	// ANTIGRAVITY_PROFILE_ID names the Antigravity gateway profile.
	ANTIGRAVITY_PROFILE_ID = "antigravity"
	// ANTIGRAVITY_BASE_URL is the production channel.
	//
	// agentsdk defaults to the daily channel and falls back to production, but
	// a proxy Profile carries exactly one host and has no retry ladder, so the
	// single choice goes to the channel this proxy has verified end to end.
	// agentsdk's own daily-first order still applies to project discovery,
	// which runs through its client rather than this profile.
	ANTIGRAVITY_BASE_URL = ag.FallbackBaseURL
	// ANTIGRAVITY_GENERATE_PATH is the blocking generation endpoint.
	ANTIGRAVITY_GENERATE_PATH = ag.PATH_GENERATE
	// ANTIGRAVITY_STREAM_PATH is the SSE generation endpoint. The gateway only
	// emits SSE when alt=sse is set; without it the response is chunked JSON.
	ANTIGRAVITY_STREAM_PATH = ag.PATH_STREAM

	// ANTIGRAVITY_TOOL_MODE_VALIDATED is the function-calling mode the
	// gateway's Claude models require.
	ANTIGRAVITY_TOOL_MODE_VALIDATED = "VALIDATED"
	// ANTIGRAVITY_GOOG_API_CLIENT mimics the Node client the IDE ships with.
	ANTIGRAVITY_GOOG_API_CLIENT = ag.GOOG_API_CLIENT
	// ANTIGRAVITY_CLIENT_NAME is the gateway's client identity header value.
	ANTIGRAVITY_CLIENT_NAME = ag.CLIENT_NAME
	// ANTIGRAVITY_CLIENT_VERSION is the IDE build advertised upstream.
	ANTIGRAVITY_CLIENT_VERSION = ag.ClientVersion
)

// antigravityUserAgent is the IDE identity the gateway matches on.
func antigravityUserAgent() string { return ag.UserAgent() }

// antigravityProjectResolver caches the Cloud Code project each credential
// bills against.
//
// The project is not part of the OAuth grant: it is issued by loadCodeAssist
// and must ride in every request envelope, so credentials minted by other
// tools arrive without one. The lookup itself belongs to agentsdk; this type
// only decides when to run it and remembers the answer, since the proxy
// resolves a credential per request and would otherwise repeat the round-trip
// on every call.
type antigravityProjectResolver struct {
	// baseURL overrides the gateway host, for tests and for credentials
	// pinned to a private gateway.
	baseURL  string
	mu       sync.Mutex
	projects map[string]string
}

func newAntigravityProjectResolver() *antigravityProjectResolver {
	return &antigravityProjectResolver{projects: make(map[string]string)}
}

// Resolve returns the project for a credential, hitting loadCodeAssist only on
// the first call for that credential.
func (r *antigravityProjectResolver) Resolve(ctx context.Context, cred *authmodel.Credential) (string, error) {
	if cred == nil {
		return "", authProxyError("antigravity credential is nil", nil)
	}
	if project := strings.TrimSpace(cred.ProjectID); project != "" {
		return project, nil
	}
	if r == nil {
		return "", unavailableUpstreamError("antigravity project resolver is unavailable", nil)
	}

	key := cred.Name()
	r.mu.Lock()
	cached, found := r.projects[key]
	r.mu.Unlock()
	if found {
		return cached, nil
	}

	project, err := r.fetchProject(ctx, cred)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.projects[key] = project
	r.mu.Unlock()
	return project, nil
}

func (r *antigravityProjectResolver) fetchProject(ctx context.Context, cred *authmodel.Credential) (string, error) {
	token := strings.TrimSpace(cred.AccessToken)
	if token == "" {
		return "", authProxyError("antigravity credential has no access token", nil)
	}

	// A credential pinned to a gateway must have its project looked up through
	// that same gateway, matching how the generation request is routed.
	baseURL := strings.TrimSpace(cred.BaseURL)
	if baseURL == "" {
		baseURL = r.baseURL
	}

	config := provider.ResolvedConfig{Auth: agcore.Auth{Bearer: token}}
	if baseURL != "" {
		config.BaseURL = strings.TrimRight(baseURL, "/")
	}
	adapter, err := ag.New(config)
	if err != nil {
		return "", unavailableUpstreamError("build antigravity project resolver", err)
	}

	project, err := adapter.ProjectID(ctx, agcore.Auth{})
	if err != nil {
		return "", &model.ProxyError{
			Kind:    model.ERROR_AUTH,
			Status:  400,
			Code:    "antigravity_project_missing",
			Message: "antigravity credential has no Cloud Code project; sign in with the Antigravity IDE once to provision it",
			Cause:   err,
		}
	}
	return project, nil
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

func normalizeAntigravityRequest(envelope model.RequestEnvelope) (NormalizedRequest, error) {
	if envelope.TargetFormat != model.FORMAT_ANTIGRAVITY {
		return NormalizedRequest{}, unsupportedFormatError(ANTIGRAVITY_PROFILE_ID, envelope.TargetFormat)
	}
	body, err := applyAntigravityToolMode(envelope.Body, envelope.Model)
	if err != nil {
		return NormalizedRequest{}, err
	}
	return NormalizedRequest{
		Body:           body,
		UpstreamStream: envelope.Stream,
	}, nil
}

// applyAntigravityToolMode forces VALIDATED function calling for the gateway's
// Claude models. Those models reject tool arguments that skip upstream
// validation, and unlike the Gemini families they do not default to it.
func applyAntigravityToolMode(body []byte, modelName string) ([]byte, error) {
	if !strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "claude") {
		return body, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, invalidRequestError("normalize antigravity request", err)
	}
	var inner map[string]json.RawMessage
	if err := json.Unmarshal(envelope["request"], &inner); err != nil {
		return nil, invalidRequestError("normalize antigravity request", err)
	}
	if _, hasTools := inner["tools"]; !hasTools {
		return body, nil
	}
	toolConfig, err := json.Marshal(map[string]any{
		"functionCallingConfig": map[string]any{"mode": ANTIGRAVITY_TOOL_MODE_VALIDATED},
	})
	if err != nil {
		return nil, fmt.Errorf("encode antigravity tool config: %w", err)
	}
	inner["toolConfig"] = toolConfig
	encodedInner, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("encode antigravity request: %w", err)
	}
	envelope["request"] = encodedInner
	return json.Marshal(envelope)
}
