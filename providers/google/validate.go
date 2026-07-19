package google

import "fmt"

// Validate runs a sanity check on the outgoing wire body before we hand
// it to net/http. Returning early here gives callers an actionable error
// instead of a vague HTTP 400 from the upstream.
//
// Returns nil on success or a descriptive error otherwise.
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("google: model is required")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("google: at least one message is required")
	}
	for i, m := range r.Messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" && m.Role != "tool" {
			return fmt.Errorf("google: message[%d] role %q must be system|user|assistant|tool", i, m.Role)
		}
	}
	return nil
}