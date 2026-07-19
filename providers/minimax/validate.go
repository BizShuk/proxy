package minimax

import "fmt"

// Validate runs the full DTO sanity check before we hand the body to the
// HTTP transport. We do this here so the wire shape stays self-documenting
// and callers get actionable errors instead of HTTP 400s with vague messages.
//
// Returns nil on success or a descriptive error otherwise.
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("minimax: model is required")
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("minimax: max_tokens must be positive (got %d)", r.MaxTokens)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("minimax: at least one message is required")
	}
	for i, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("minimax: message[%d] role %q must be user|assistant (system goes to top-level)", i, m.Role)
		}
		if len(m.Content) == 0 {
			return fmt.Errorf("minimax: message[%d] has no content blocks", i)
		}
	}
	return nil
}
