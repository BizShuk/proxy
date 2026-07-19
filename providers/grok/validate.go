package grok

import "fmt"

// Validate runs the full DTO sanity check before we hand the body to the
// HTTP transport. We do this here so the wire shape stays self-documenting
// and so callers get actionable errors instead of HTTP 400s with vague
// messages.
//
// Returns nil on success or a descriptive error otherwise.
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("grok: model is required")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("grok: at least one message is required")
	}
	for i, m := range r.Messages {
		switch m.Role {
		case "system", "user", "assistant", "tool":
			// ok
		default:
			return fmt.Errorf("grok: message[%d] role %q must be system|user|assistant|tool", i, m.Role)
		}
		if m.Content == "" && len(m.ToolCalls) == 0 && m.ToolCallID == "" {
			return fmt.Errorf("grok: message[%d] has empty content", i)
		}
	}
	return nil
}