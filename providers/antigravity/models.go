package antigravity

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog returns the bundled Antigravity model catalog.
//
// Models served via the Antigravity gateway as documented by CLIProxyAPI:
//
//	https://help.router-for-me/configuration/provider/antigravity
//
// Both Claude and Gemini families are routed through the gateway. IDs are
// the strings the gateway accepts on the wire; Family is a coarse bucket
// for picker grouping; Reasoning reflects whether the model supports
// extended thinking / chain-of-thought output.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		// Claude family (Anthropic-Messages path).
		{ID: "claude-opus-4-8", Family: "claude-opus", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 200000, MaxTokens: 32000},
		{ID: "claude-sonnet-5", Family: "claude-sonnet", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 200000, MaxTokens: 8192},

		// Gemini family (often served via the OpenAI-compat or
		// Anthropic-compat path through the same gateway).
		{ID: "gemini-2.5-pro", Family: "gemini-pro", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE, core.MODALITY_AUDIO},
			ContextWindow: 1000000, MaxTokens: 8192},
		{ID: "gemini-2.0-flash", Family: "gemini-flash", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE, core.MODALITY_AUDIO},
			ContextWindow: 1000000, MaxTokens: 8192},
	}
}