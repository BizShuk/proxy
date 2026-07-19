// Static model catalog for xAI Grok. Mirrors core.ModelSpec so picker
// UIs and budget middleware can plan across providers without reaching
// into provider-specific types.

package grok

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog returns the bundled xAI Grok model catalog as of
// 2026-07.
//
// IDs are xAI's published model strings. Family is a coarse bucket used
// for picker grouping. Reasoning reflects whether the model emits visible
// "thinking" tokens before its final answer (grok-3-mini and grok-4 do).
//
// This list is intentionally conservative — adding a new model here is
// a user-facing change because picker UIs render it.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		{
			ID:            "grok-4",
			Family:        "grok-4",
			Reasoning:     true,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 256000,
			MaxTokens:     8192,
		},
		{
			ID:            "grok-3",
			Family:        "grok-3",
			Reasoning:     false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 131072,
			MaxTokens:     8192,
		},
		{
			ID:            "grok-3-mini",
			Family:        "grok-3-mini",
			Reasoning:     true,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 131072,
			MaxTokens:     8192,
		},
		{
			ID:            "grok-2-vision",
			Family:        "grok-2",
			Reasoning:     false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 32768,
			MaxTokens:     8192,
		},
	}
}