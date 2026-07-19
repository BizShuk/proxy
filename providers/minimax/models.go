package minimax

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog returns the bundled minimax model catalog.
//
// Model ids mirror what the proxy/svc/upstream/profile.go advertises
// under the "minimax" provider ("MiniMax-Text-01" and the "minimax-*"
// prefix). Reasoning reflects whether the model is in the extended
// thinking family; ContextWindow / MaxTokens are best-effort estimates
// from the public docs — callers needing exact limits should consult
// the API docs at https://docs.minimax.io directly.
//
// This list is intentionally conservative — adding a new model here is
// a user-facing change because picker UIs render it. Add new models in
// a follow-up after they ship a stable API.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		// minimax-M2 family — current flagship, supports reasoning.
		{ID: "minimax-M2", Family: "minimax-M2", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 256000, MaxTokens: 16384},
		// MiniMax-Text-01 — base text model, no reasoning.
		{ID: "MiniMax-Text-01", Family: "minimax-Text", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
	}
}
