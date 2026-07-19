package anthropic

import "fmt"

// Validate runs the full DTO sanity check before we hand the body to the
// SDK. We do this here so the wire shape stays self-documenting and so
// callers get actionable errors instead of HTTP 400s with vague messages.
//
// Returns nil on success or a descriptive error otherwise.
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("anthropic: model is required")
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("anthropic: max_tokens must be positive (got %d)", r.MaxTokens)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("anthropic: at least one message is required")
	}
	for i, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("anthropic: message[%d] role %q must be user|assistant (system goes to top-level)", i, m.Role)
		}
		if len(m.Content) == 0 {
			return fmt.Errorf("anthropic: message[%d] has no content blocks", i)
		}
	}
	return nil
}
