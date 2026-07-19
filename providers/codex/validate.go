package codex

import "fmt"

// Validate runs the DTO sanity check before we POST the body to the
// Codex endpoint. We do this so callers see an actionable Go error
// instead of an opaque HTTP 400 from chatgpt.com.
//
// The rules here mirror the upstream schema's hard requirements:
//   - model is required (Codex dispatches on it server-side)
//   - at least one of instructions or input[] must be non-empty
//     (Codex rejects an empty payload as "no content to respond to")
func (r RequestBody) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("codex: model is required")
	}
	if r.Instructions == "" && len(r.Input) == 0 {
		return fmt.Errorf("codex: at least one of instructions or input is required")
	}
	return nil
}
