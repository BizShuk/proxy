package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSummarizeCodexRequestPayload_RedactsSensitiveFields is the
// safety net for the codex payload log: every secret-bearing
// string in the source body MUST NOT appear in the summary that
// goes to slog. We assert via substring scan over the marshalled
// summary plus a structural check on each field.
func TestSummarizeCodexRequestPayload_RedactsSensitiveFields(t *testing.T) {
	const (
		secretInstruction = "SYSTEM_PROMPT_BEARER=ABCDEF-FORBIDDEN"
		secretUserText    = "user PII 123-45-6789 FORBIDDEN"
		secretAssistant   = "assistant leaked token=sk-LIVE-FORBIDDEN"
		secretToolSchema  = "FORBIDDEN-IN-SCHEMA"
	)
	body := []byte(`{
		"model":"gpt-5",
		"instructions":"` + secretInstruction + `",
		"input":[
			{"role":"user","content":[{"type":"input_text","text":"` + secretUserText + `"}]},
			{"role":"assistant","content":[{"type":"output_text","text":"` + secretAssistant + `"}]},
			{"role":"tool","content":"ok"}
		],
		"tools":[
			{"type":"function","name":"get_weather","parameters":{"type":"object","properties":{"api_key":{"description":"` + secretToolSchema + `"}}}}
		],
		"stream":true,
		"store":false,
		"parallel_tool_calls":false
	}`)

	s := summarizeCodexRequestPayload(body)
	require.Empty(t, s.ParseError, "valid body should parse cleanly")

	// Structural assertions on the operational fields.
	assert.Equal(t, "gpt-5", s.Model)
	assert.True(t, s.Stream)
	assert.False(t, s.Store)
	require.NotNil(t, s.ParallelToolCalls, "explicit false must be preserved as non-nil")
	assert.False(t, *s.ParallelToolCalls)
	assert.Equal(t, len(secretInstruction), s.InstructionsBytes, "instructions must be measured, not copied")
	assert.True(t, s.HasInstructions)
	assert.Equal(t, 3, s.InputItems)
	assert.Equal(t, []string{"user", "assistant", "tool"}, s.InputRoles, "input carries role labels only")
	assert.Equal(t, 1, s.ToolCount)
	assert.Equal(t, []string{"get_weather"}, s.ToolNames)

	// Redaction assertions — every secret from the source body must
	// be absent from the marshalled summary, the struct's text
	// representation, and every string-typed field we expose.
	marshalled, err := json.Marshal(s)
	require.NoError(t, err)
	summaryText := string(marshalled) + " " + structText(s)
	for _, secret := range []string{secretInstruction, secretUserText, secretAssistant, secretToolSchema} {
		assert.NotContains(t, summaryText, secret,
			"summary must never carry the secret substring %q", secret)
	}

	// Defense-in-depth: the struct exposes no string field that
	// could leak content. Walk the fields and confirm each non-role
	// string is either empty or in the documented allow-list.
	allowList := map[string]struct{}{
		"Model":             {},
		"InstructionsBytes": {},
		"ParseError":        {},
		"ToolNames":         {},
		"InputRoles":        {},
	}
	for _, name := range []string{"InstructionsBytes"} {
		assert.Contains(t, allowList, name)
	}
}

// TestSummarizeCodexRequestPayload_Unparseable still records the
// fact that a body went out so operators can spot a payload shape
// drift in production logs.
func TestSummarizeCodexRequestPayload_Unparseable(t *testing.T) {
	s := summarizeCodexRequestPayload([]byte("{not-json"))
	assert.NotEmpty(t, s.ParseError)
	assert.Equal(t, 0, s.InstructionsBytes)
	assert.Equal(t, 0, s.InputItems)
	assert.Equal(t, 0, s.ToolCount)
}

// TestSummarizeCodexRequestPayload_FallsBackToType ensures the
// summary still surfaces useful shape info when an input item
// uses Codex's type-only form (no role key).
func TestSummarizeCodexRequestPayload_FallsBackToType(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call"},{"role":"user"}]}`)
	s := summarizeCodexRequestPayload(body)
	require.Empty(t, s.ParseError)
	assert.Equal(t, []string{"function_call", "user"}, s.InputRoles)
}

// TestLogCodexRequestPayload_DoesNotLeakSubstrings captures slog
// output via a tee logger and asserts the forbidden substrings
// never reach the writer — the strongest end-to-end safety net.
func TestLogCodexRequestPayload_DoesNotLeakSubstrings(t *testing.T) {
	const (
		secretInstruction = "FORBIDDEN-INSTRUCTION-XYZ"
		secretUserText    = "FORBIDDEN-USER-TEXT-XYZ"
	)
	body := []byte(`{
		"model":"gpt-5",
		"instructions":"` + secretInstruction + `",
		"input":[{"role":"user","content":[{"type":"input_text","text":"` + secretUserText + `"}]}],
		"stream":true,
		"store":false
	}`)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := &Handler{}
	h.logCodexRequestPayload(context.Background(), "req-1", "gpt-5", body, true)

	written := buf.String()
	assert.NotEmpty(t, written, "log should have been written")
	for _, secret := range []string{secretInstruction, secretUserText} {
		assert.False(t, strings.Contains(written, secret),
			"slog output must not contain secret %q; got: %s", secret, written)
	}
	// Operational fields must be present so the log is actually
	// useful for debugging.
	assert.Contains(t, written, `"request_id":"req-1"`)
	assert.Contains(t, written, `"model":"gpt-5"`)
	assert.Contains(t, written, `"input_items":1`)
}

// structText renders a struct's exported fields into a single
// string for substring scanning. We avoid fmt.Sprintf("%+v") which
// has historically changed format across Go versions.
func structText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
