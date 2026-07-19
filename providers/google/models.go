package google

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog lists the Google Generative AI models the proxy
// serves by default. Three families are present:
//
//   - gemini-*  : Gemini chat + embeddings. Reasoning flag mirrors
//                 Google's "thinking" flag where applicable.
//   - gemma-*   : Gemma open chat models.
//   - imagen-*  : Imagen image generation (separate OpenAI-compat
//                 surface — see /v1beta/openai for coverage).
//
// Embedding and image-generation models are listed for catalog
// completeness; the proxy's chat-completions adapter serves them
// through the same wire shape so callers see a single /v1/models
// response. ContextWindow / MaxTokens are zero for non-chat models
// since they don't apply to embeddings or image generation.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		// -----------------------------------------------------------------
		// Gemma 4 — open chat models
		// -----------------------------------------------------------------
		{ID: "gemma-4-31b-it", Family: "gemma", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
		{ID: "gemma-4-26b-a4b-it", Family: "gemma", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},

		// -----------------------------------------------------------------
		// Gemini Embedding — text embeddings
		// -----------------------------------------------------------------
		{ID: "gemini-embedding-2", Family: "gemini-embedding", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 8192, MaxTokens: 0},
		{ID: "gemini-embedding-2-preview", Family: "gemini-embedding", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 8192, MaxTokens: 0},
		{ID: "gemini-embedding-001", Family: "gemini-embedding", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 2048, MaxTokens: 0},

		// -----------------------------------------------------------------
		// Gemini 3.x — chat models
		// -----------------------------------------------------------------
		{ID: "gemini-3.1-flash-lite", Family: "gemini-flash", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 1048576, MaxTokens: 65536},
		{ID: "gemini-3.5-flash", Family: "gemini-flash", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 1048576, MaxTokens: 65536},
		{ID: "gemini-3-flash-preview", Family: "gemini-flash", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 1048576, MaxTokens: 65536},

		// -----------------------------------------------------------------
		// Imagen 4 — image generation
		// -----------------------------------------------------------------
		{ID: "imagen-4.0-fast-generate-001", Family: "imagen", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 0, MaxTokens: 0},
		{ID: "imagen-4.0-generate-001", Family: "imagen", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 0, MaxTokens: 0},
		{ID: "imagen-4.0-ultra-generate-001", Family: "imagen", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 0, MaxTokens: 0},
	}
}