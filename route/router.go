package route

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/protocol"
)

// Route is the deterministic provider-family decision for one request.
type Route struct {
	ProviderID   string
	Model        string
	SourceFormat protocol.Format
	ForcedTarget *protocol.Format
}

// Router resolves qualified, exact, and anchored-prefix model names.
type Router struct {
	profiles   []Profile
	qualifiers map[string]string
}

// NewRouter validates and copies provider-family routing profiles.
func NewRouter(profiles []Profile) (*Router, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("route router: no profiles")
	}

	router := &Router{
		profiles:   make([]Profile, 0, len(profiles)),
		qualifiers: make(map[string]string),
	}
	profileIDs := make(map[string]struct{}, len(profiles))
	for index, source := range profiles {
		profile, err := normalizeProfile(source)
		if err != nil {
			return nil, fmt.Errorf("route router profile %d: %w", index, err)
		}
		if _, exists := profileIDs[profile.ID]; exists {
			return nil, fmt.Errorf("route router: duplicate profile ID %q", source.ID)
		}
		profileIDs[profile.ID] = struct{}{}
		for _, qualifier := range profile.Qualifiers {
			if owner, exists := router.qualifiers[qualifier]; exists {
				return nil, fmt.Errorf("route router: duplicate qualifier %q for %q and %q", qualifier, owner, profile.ID)
			}
			router.qualifiers[qualifier] = profile.ID
		}
		router.profiles = append(router.profiles, profile)
	}
	return router, nil
}

// Resolve maps a model name to exactly one provider family.
func (r *Router) Resolve(sourceFormat protocol.Format, model string) (Route, error) {
	if r == nil {
		return Route{}, unknownModelError(model, "router is nil")
	}
	trimmedModel := strings.TrimSpace(model)
	if trimmedModel == "" {
		return Route{}, unknownModelError(model, "model is blank")
	}

	if qualifier, routedModel, qualified := strings.Cut(trimmedModel, "/"); qualified {
		providerID, exists := r.qualifiers[strings.ToLower(strings.TrimSpace(qualifier))]
		if !exists || strings.TrimSpace(routedModel) == "" {
			return Route{}, unknownModelError(model, "unknown or incomplete provider qualifier")
		}
		route := Route{
			ProviderID:   providerID,
			Model:        routedModel,
			SourceFormat: sourceFormat,
		}
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(qualifier)), "-chat") {
			forced := protocol.FORMAT_OPENAI_CHAT
			route.ForcedTarget = &forced
		}
		return route, nil
	}

	normalizedModel := strings.ToLower(trimmedModel)
	if matches := r.exactMatches(normalizedModel); len(matches) != 0 {
		return matchRoute(matches, trimmedModel, sourceFormat)
	}
	return matchRoute(r.prefixMatches(normalizedModel), trimmedModel, sourceFormat)
}

func normalizeProfile(source Profile) (Profile, error) {
	profile := Profile{ID: strings.ToLower(strings.TrimSpace(source.ID))}
	if profile.ID == "" {
		return Profile{}, fmt.Errorf("profile ID is blank")
	}

	var err error
	profile.Qualifiers, err = normalizeKeys(source.Qualifiers, "qualifier", true)
	if err != nil {
		return Profile{}, err
	}
	profile.ExactModels, err = normalizeKeys(source.ExactModels, "exact model", false)
	if err != nil {
		return Profile{}, err
	}
	profile.Prefixes, err = normalizeKeys(source.Prefixes, "prefix", true)
	if err != nil {
		return Profile{}, err
	}
	return profile, nil
}

func normalizeKeys(values []string, label string, rejectEmpty bool) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "" {
			if rejectEmpty {
				return nil, fmt.Errorf("%s is blank", label)
			}
			continue
		}
		if _, exists := seen[normalized]; exists {
			return nil, fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func (r *Router) exactMatches(model string) []string {
	var matches []string
	for _, profile := range r.profiles {
		for _, exactModel := range profile.ExactModels {
			if model == exactModel {
				matches = append(matches, profile.ID)
				break
			}
		}
	}
	return matches
}

func (r *Router) prefixMatches(model string) []string {
	var matches []string
	for _, profile := range r.profiles {
		for _, prefix := range profile.Prefixes {
			if strings.HasPrefix(model, prefix) {
				matches = append(matches, profile.ID)
				break
			}
		}
	}
	return matches
}

func matchRoute(matches []string, model string, sourceFormat protocol.Format) (Route, error) {
	if len(matches) != 1 {
		reason := "no provider matched"
		if len(matches) > 1 {
			reason = "multiple providers matched"
		}
		return Route{}, unknownModelError(model, reason)
	}
	return Route{ProviderID: matches[0], Model: model, SourceFormat: sourceFormat}, nil
}

func unknownModelError(model, reason string) error {
	return &protocol.ProxyError{
		Kind:    protocol.ERROR_UNKNOWN_MODEL,
		Status:  http.StatusBadRequest,
		Code:    "unknown_model",
		Message: fmt.Sprintf("cannot route model %q: %s", model, reason),
	}
}
