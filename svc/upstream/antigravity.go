package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	authmodel "github.com/bizshuk/auth/model"
	"github.com/bizshuk/proxy/model"
)

// Antigravity's only deviation from the generic upstream path: its envelope
// carries the caller's Cloud Code project in the body, so it fills the
// Profile.ApplyCredentialBody hook.
//
// The project itself is auth's: it is account provisioning, not an inference
// parameter, so auth resolves it during login and stores it on the credential.
// The protocol — endpoints, client identity, host fallback, the envelope and
// the schema dialect — is agentsdk's. Nothing here re-derives either.

// applyAntigravityCredentialBody stamps the credential's Cloud Code project
// onto the request envelope. The protocol transform cannot do it: the project
// comes from the credential, which only the transport layer holds.
func applyAntigravityCredentialBody(
	_ context.Context,
	cred *authmodel.Credential,
	body []byte,
) ([]byte, error) {
	project := strings.TrimSpace(cred.ProjectID)
	if project == "" {
		return nil, &model.ProxyError{
			Kind:    model.ERROR_AUTH,
			Status:  http.StatusBadRequest,
			Code:    "antigravity_project_missing",
			Message: "antigravity credential has no Cloud Code project; run the login again to provision it",
		}
	}
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
