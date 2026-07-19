package ollama

import "github.com/bizshuk/agentsdk/core"

// DefaultCatalog lists common Ollama / OpenAI-compatible model ids.
//
// Most users override this via WithModel or a viper-loaded config; this
// is the fallback set the picker UI sees when nothing else is supplied.
//
// Reasoning = true flags the "thinking" family (gpt-oss / deepseek-r1);
// picker UIs can use that to expose reasoning budget controls.
func DefaultCatalog() []core.ModelSpec {
	return []core.ModelSpec{
		{ID: "llama3.2", Family: "llama", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
		{ID: "qwen2.5", Family: "qwen", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
		{ID: "mistral", Family: "mistral", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 32000, MaxTokens: 8192},
		{ID: "gpt-oss:20b", Family: "gpt-oss", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
		{ID: "deepseek-r1", Family: "deepseek", Reasoning: true,
			Input:         []core.Modality{core.MODALITY_TEXT},
			ContextWindow: 128000, MaxTokens: 8192},
		{ID: "llava", Family: "llava", Reasoning: false,
			Input:         []core.Modality{core.MODALITY_TEXT, core.MODALITY_IMAGE},
			ContextWindow: 32000, MaxTokens: 4096},
	}
}