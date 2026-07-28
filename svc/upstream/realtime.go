package upstream

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
)

const (
	OPENAI_REALTIME_WEBSOCKET_ENDPOINT      = "/v1/realtime"
	OPENAI_REALTIME_CALLS_ENDPOINT          = "/v1/realtime/calls"
	OPENAI_REALTIME_CLIENT_SECRETS_ENDPOINT = "/v1/realtime/client_secrets"
	OPENAI_SAFETY_IDENTIFIER_HEADER         = "OpenAI-Safety-Identifier"
)

var openAIRealtimeEndpoints = map[string]struct{}{
	OPENAI_REALTIME_WEBSOCKET_ENDPOINT:      {},
	OPENAI_REALTIME_CALLS_ENDPOINT:          {},
	OPENAI_REALTIME_CLIENT_SECRETS_ENDPOINT: {},
}

// RealtimeTarget is a credential-bound, fixed-path OpenAI Realtime upstream.
// It intentionally does not expose the credential or provider profile.
type RealtimeTarget struct {
	URL            *url.URL
	RequestHeaders http.Header
	profile        Profile
}

// PrepareOpenAIRealtimeTarget validates a standard OpenAI API-key credential,
// fixes the upstream path, and replaces downstream authentication headers.
func PrepareOpenAIRealtimeTarget(
	catalog *Catalog,
	credential *ResolvedCredential,
	endpoint string,
	sourceHeaders http.Header,
) (RealtimeTarget, error) {
	if catalog == nil {
		return RealtimeTarget{}, unavailableUpstreamError("upstream catalog is unavailable", nil)
	}
	endpoint = strings.TrimSpace(endpoint)
	if _, ok := openAIRealtimeEndpoints[endpoint]; !ok {
		return RealtimeTarget{}, &model.ProxyError{
			Kind:    model.ERROR_INVALID_REQUEST,
			Status:  http.StatusBadRequest,
			Code:    "invalid_realtime_endpoint",
			Message: fmt.Sprintf("unsupported realtime endpoint %q", endpoint),
		}
	}
	if credential == nil {
		return RealtimeTarget{}, authProxyError("OpenAI Realtime credential is nil", nil)
	}
	if credential.Kind != authmodel.KIND_API_KEY {
		return RealtimeTarget{}, &model.ProxyError{
			Kind:    model.ERROR_AUTH,
			Status:  http.StatusUnauthorized,
			Code:    "realtime_api_key_required",
			Message: "OpenAI Realtime requires a standard API-key credential",
		}
	}

	profile, ok := catalog.Lookup("openai-api")
	if !ok {
		return RealtimeTarget{}, unavailableUpstreamError("OpenAI API profile is unavailable", nil)
	}
	if err := validateCredentialForProfile(profile, credential); err != nil {
		return RealtimeTarget{}, err
	}

	baseURL := profile.BaseURL
	if strings.TrimSpace(credential.BaseURL) != "" {
		baseURL = credential.BaseURL
	}
	requestURL, err := buildEndpointURL(baseURL, endpoint)
	if err != nil {
		return RealtimeTarget{}, unavailableUpstreamError("invalid OpenAI Realtime endpoint", err)
	}
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return RealtimeTarget{}, unavailableUpstreamError("parse OpenAI Realtime endpoint", err)
	}

	headers := make(http.Header)
	forwardAllowlistedHeaders(profile, sourceHeaders, headers)
	applyProviderHeaders(profile, credential, headers)
	return RealtimeTarget{
		URL:            parsedURL,
		RequestHeaders: headers,
		profile:        profile,
	}, nil
}

// SanitizeResponseHeaders returns only profile-allowlisted response headers.
func (target RealtimeTarget) SanitizeResponseHeaders(source http.Header) http.Header {
	headers := make(http.Header)
	for name, values := range source {
		if !target.profile.AllowsResponseHeader(name) {
			continue
		}
		for _, value := range values {
			headers.Add(name, value)
		}
	}
	return headers
}
