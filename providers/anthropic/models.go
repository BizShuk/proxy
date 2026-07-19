package anthropic

// Static model catalog the SDK ships by default. Mirrors core.ModelSpec
// so picker UIs and budget middleware can plan across providers without
// reaching into Anthropic-specific types.

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog returns the bundled Anthropic model catalog.
//
// IDs are Anthropic's published model strings. Family is a coarse bucket
// used for picker grouping; Reasoning reflects whether the model is in
// the "extended thinking" family.
//
// This list is intentionally conservative — adding a new model here is a
// user-facing change because picker UIs render it. Add new models in a
// follow-up after they ship a stable API.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		{ID: "claude-opus-4-8", Family: "claude-opus", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 200000, MaxTokens: 32000},
		{ID: "claude-sonnet-5", Family: "claude-sonnet", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 200000, MaxTokens: 8192},
		{ID: "claude-haiku-4-5-20251001", Family: "claude-haiku", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 200000, MaxTokens: 8192},
	}
}
