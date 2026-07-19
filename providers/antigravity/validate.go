package antigravity

import "fmt"

// Validate runs the DTO sanity check before we hand the body to the HTTP
// client. We do this so the wire shape stays self-documenting and so callers
// get actionable errors instead of HTTP 400s with vague messages.
//
// Returns nil on success or a descriptive error otherwise.
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("antigravity: model is required")
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("antigravity: max_tokens must be positive (got %d)", r.MaxTokens)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("antigravity: at least one message is required")
	}
	for i, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" {
			return fmt.Errorf("antigravity: message[%d] role %q must be user|assistant (system goes to top-level)", i, m.Role)
		}
		if len(m.Content) == 0 {
			return fmt.Errorf("antigravity: message[%d] has no content blocks", i)
		}
	}
	return nil
}