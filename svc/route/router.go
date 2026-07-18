package route

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/bizshuk/proxy/model"
)

// Route is the deterministic provider-family decision for one request.
type Route struct {
	ProviderID   string
	Model        string
	SourceFormat model.Format
	ForcedTarget *model.Format
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
func (r *Router) Resolve(sourceFormat model.Format, modelName string) (Route, error) {
	if r == nil {
		return Route{}, unknownModelError(modelName, "router is nil")
	}
	trimmedModel := strings.TrimSpace(modelName)
	if trimmedModel == "" {
		return Route{}, unknownModelError(modelName, "model is blank")
	}

	if qualifier, routedModel, qualified := strings.Cut(trimmedModel, "/"); qualified {
		providerID, exists := r.qualifiers[strings.ToLower(strings.TrimSpace(qualifier))]
		if !exists || strings.TrimSpace(routedModel) == "" {
			return Route{}, unknownModelError(modelName, "unknown or incomplete provider qualifier")
		}
		route := Route{
			ProviderID:   providerID,
			Model:        routedModel,
			SourceFormat: sourceFormat,
		}
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(qualifier)), "-chat") {
			forced := model.FORMAT_OPENAI_CHAT
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

func (r *Router) exactMatches(modelName string) []string {
	var matches []string
	for _, profile := range r.profiles {
		for _, exactModel := range profile.ExactModels {
			if modelName == exactModel {
				matches = append(matches, profile.ID)
				break
			}
		}
	}
	return matches
}

func (r *Router) prefixMatches(modelName string) []string {
	var matches []string
	for _, profile := range r.profiles {
		for _, prefix := range profile.Prefixes {
			if strings.HasPrefix(modelName, prefix) {
				matches = append(matches, profile.ID)
				break
			}
		}
	}
	return matches
}

func matchRoute(matches []string, modelName string, sourceFormat model.Format) (Route, error) {
	if len(matches) != 1 {
		reason := "no provider matched"
		if len(matches) > 1 {
			reason = "multiple providers matched"
		}
		return Route{}, unknownModelError(modelName, reason)
	}
	return Route{ProviderID: matches[0], Model: modelName, SourceFormat: sourceFormat}, nil
}

func unknownModelError(modelName, reason string) error {
	return &model.ProxyError{
		Kind:    model.ERROR_UNKNOWN_MODEL,
		Status:  http.StatusBadRequest,
		Code:    "unknown_model",
		Message: fmt.Sprintf("cannot route model %q: %s", modelName, reason),
	}
}
