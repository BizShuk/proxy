# Pairwise Agent–Provider Transform Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the one-file proxy adaptor with a complete 3×3 pairwise protocol transform registry, provider-capability routing, safe upstream transport, and correct request/non-stream/stream response handling for Anthropic Messages, OpenAI Chat Completions, and OpenAI Responses.

**Architecture:** Keep pairwise transforms: each directed `(source format, target format)` registration owns request translation plus reverse non-stream and streaming response translation. Keep provider vendor concerns separate: routing selects a provider family, credential resolution selects a concrete provider profile, and the upstream layer applies endpoint/auth/header/timeout rules. Existing `core.ModelRequest`, `core.ModelResult`, and runtime provider ports remain unchanged.

**Tech Stack:** Go 1.26.0, stdlib `net/http`/`encoding/json`/`bufio`, Gin 1.11.0, `testify` 1.11.1, existing `auth.FileStore` and `auth/provider` registry, `httptest.Server`, table-driven tests, `go test -race`.

## Global Constraints

| Rule | Required value |
|---|---|
| Go version | `1.26.0` |
| Protocol formats | Exactly `anthropic-messages`, `openai-chat`, `openai-responses` |
| Pair coverage | Exactly 9 directed pairs; each has request, non-stream response, and stream factory |
| Provider profiles | `anthropic`, `minimax`, `openai-api`, `openai-codex-oauth`, `xai` |
| xAI default | OpenAI Responses at `/v1/responses`; Chat remains available when explicitly forced |
| Registry wiring | Explicit constructor wiring; no package `init()` registration and no mutable global registry |
| Core boundary | Do not modify `core.ModelRequest`, `core.ModelResult`, `core.ModelProvider`, or runtime provider ports |
| Protocol dependencies | `proxy/protocol` imports stdlib only; transform code never reads credentials or performs HTTP |
| Semantic loss | Convert, record a typed warning/loss, or reject; never silently drop unsupported semantics |
| Error handling | Check and wrap JSON, refresh, save, HTTP, SSE, read, write, and flush-related errors |
| Logging | Never log complete prompts, tool results, credentials, `Authorization`, or `x-api-key` values |
| Naming | Exported Go names use `MixedCaps`; constants use project-standard `SCREAMING_SNAKE_CASE` |
| Test style | Table-driven tests with `t.Run`, `testify/assert`, and `testify/require` |
| Commit cadence | One commit per task; stage only files listed by that task |
| Current baseline | `go test ./proxy/... -count=1` passes; `go test ./...` has the unrelated existing failure `app.TestRunRejectsEmptyName` |
| User worktree | Preserve the existing unstaged `README.todo`; never overwrite or stage unrelated user changes |

## File Map

### New protocol files

| File | Responsibility |
|---|---|
| `proxy/protocol/format.go` | Format constants, all-format list, request metadata extraction |
| `proxy/protocol/envelope.go` | Request/response/exchange envelopes and semantic warning types |
| `proxy/protocol/error.go` | Typed proxy errors and source-format error JSON encoders |
| `proxy/protocol/sse.go` | Full SSE frame parser/writer with multiline data and unexpected-EOF detection |
| `proxy/protocol/anthropic/types.go` | Anthropic request/response/content/tool DTOs and content unmarshal rules |
| `proxy/protocol/chat/types.go` | Chat request/response/chunk/tool DTOs |
| `proxy/protocol/responses/types.go` | Responses request/response/output/tool/usage DTOs |

### New transform files

| File | Responsibility |
|---|---|
| `proxy/transform/types.go` | Transform function types, pair contract, stream contract |
| `proxy/transform/registry.go` | Duplicate/nil/9-pair coverage validation and lookup |
| `proxy/transform/identity.go` | Three validated identity pairs |
| `proxy/transform/helpers_test.go` | Shared fixture, exchange, frame, and semantic assertion helpers for transform tests |
| `proxy/transform/anthropic_chat_request.go` | Anthropic ↔ Chat requests |
| `proxy/transform/anthropic_responses_request.go` | Anthropic ↔ Responses requests |
| `proxy/transform/chat_responses_request.go` | Chat ↔ Responses requests |
| `proxy/transform/anthropic_chat_response.go` | Anthropic ↔ Chat non-stream responses |
| `proxy/transform/anthropic_responses_response.go` | Anthropic ↔ Responses non-stream responses |
| `proxy/transform/chat_responses_response.go` | Chat ↔ Responses non-stream responses |
| `proxy/transform/response.go` | Bounded upstream error decoding and shared response mappings |
| `proxy/transform/anthropic_chat_stream.go` | Anthropic ↔ Chat stream state machines |
| `proxy/transform/anthropic_responses_stream.go` | Anthropic ↔ Responses stream state machines |
| `proxy/transform/chat_responses_stream.go` | Chat ↔ Responses stream state machines |
| `proxy/transform/collector.go` | Fold a complete source-format SSE lifecycle into its equivalent non-stream JSON |
| `proxy/transform/default.go` | Explicit assembly of all 9 production pairs |

### New routing and upstream files

| File | Responsibility |
|---|---|
| `proxy/route/router.go` | Qualified/exact/prefix model routing with no default fallback |
| `proxy/route/profile.go` | Provider-family routing metadata and resolved route type |
| `proxy/upstream/profile.go` | Concrete endpoint/auth/normalizer profiles and catalog |
| `proxy/upstream/credential.go` | Active credential selection, env fallback, refresh, required save |
| `proxy/upstream/client.go` | Request construction, header allowlist, injected HTTP client/timeouts |

### New/modified integration files

| File | Responsibility |
|---|---|
| `proxy/handler.go` | Generic route → transform → upstream → reverse-transform pipeline |
| `proxy/handler_test.go` | 21 provider routing cases plus errors, cancellation, and token counting |
| `proxy/observability.go` | Redacted structured transform logs and OpenTelemetry warning/loss counters |
| `proxy/server.go` | Construct dependencies, fail startup on invalid registry, wire generic handlers |
| `proxy/server_test.go` | Constructor and route-surface tests |
| `cmd/proxy.go` | Handle the new error-returning `proxy.New` constructor |
| `README.md` | Proxy format/provider behavior and request flow |
| `CLAUDE.md` | New proxy package tree and architectural decisions |

### Removed after cutover

- `proxy/adaptor/adaptor.go`
- `proxy/adaptor/adaptor_test.go`
- `proxy/adaptor/translator.go`
- `proxy/adaptor/translator_test.go`

---

### Task 1: Protocol envelopes, errors, and SSE framing

**Files:**

- Create: `proxy/protocol/format.go`
- Create: `proxy/protocol/envelope.go`
- Create: `proxy/protocol/error.go`
- Create: `proxy/protocol/sse.go`
- Create: `proxy/protocol/protocol_test.go`
- Create: `proxy/protocol/sse_test.go`

**Interfaces:**

- Consumes: stdlib only.
- Produces: `Format`, `ALL_FORMATS`, `ParseRequestMeta`, `RequestEnvelope`, `TransformResult`, `Exchange`, `ResponseEnvelope`, `ProxyError`, `EncodeError`, `SSEFrame`, `SSEDecoder.Next`, and `WriteSSE`.

- [ ] **Step 1: Write failing format/envelope/error tests**

```go
func TestParseRequestMeta(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantModel  string
		wantStream bool
		wantErr    bool
	}{
		{"chat", `{"model":"gpt-4o","stream":true}`, "gpt-4o", true, false},
		{"responses omitted stream", `{"model":"gpt-5","input":"hi"}`, "gpt-5", false, false},
		{"missing model", `{"stream":true}`, "", false, true},
		{"malformed", `{`, "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model, stream, err := ParseRequestMeta([]byte(tc.body))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantModel, model)
			assert.Equal(t, tc.wantStream, stream)
		})
	}
}

func TestEncodeErrorUsesSourceFormat(t *testing.T) {
	proxyErr := &ProxyError{Kind: ERROR_RATE_LIMIT, Status: 429, Code: "rate_limit_exceeded", Message: "slow down"}
	anthropicBody, err := EncodeError(FORMAT_ANTHROPIC_MESSAGES, proxyErr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`, string(anthropicBody))

	chatBody, err := EncodeError(FORMAT_OPENAI_CHAT, proxyErr)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"slow down"}}`, string(chatBody))
}
```

- [ ] **Step 2: Run the tests and confirm the package does not exist yet**

Run: `go test ./proxy/protocol -run 'TestParseRequestMeta|TestEncodeErrorUsesSourceFormat' -count=1`

Expected: FAIL because `proxy/protocol` and its exported contracts have not been implemented.

- [ ] **Step 3: Implement the protocol contracts**

```go
type Format string

const (
	FORMAT_ANTHROPIC_MESSAGES Format = "anthropic-messages"
	FORMAT_OPENAI_CHAT        Format = "openai-chat"
	FORMAT_OPENAI_RESPONSES   Format = "openai-responses"
)

var ALL_FORMATS = []Format{
	FORMAT_ANTHROPIC_MESSAGES,
	FORMAT_OPENAI_CHAT,
	FORMAT_OPENAI_RESPONSES,
}

type RequestEnvelope struct {
	SourceFormat Format
	TargetFormat Format
	Model        string
	Stream       bool
	Headers      http.Header
	Body         []byte
}

type Warning struct{ Code, Message string }
type SemanticLoss struct{ Field, Reason string }

type TransformResult struct {
	Body     []byte
	Warnings []Warning
	Losses   []SemanticLoss
}

type Exchange struct {
	OriginalRequest   RequestEnvelope
	TranslatedRequest RequestEnvelope
	ProviderID        string
	NewID             func() string
}

type ResponseEnvelope struct {
	Status   int
	Headers  http.Header
	Body     []byte
	Exchange Exchange
}

type ProxyError struct {
	Kind              ErrorKind
	Status            int
	Code              string
	Message           string
	RetryAfter        time.Duration
	UpstreamRequestID string
	Cause             error
}

type SSEFrame struct {
	Event       string
	ID          string
	RetryMillis *int
	Comments    []string
	Data        []byte
}
```

Implement `ParseRequestMeta` with a small anonymous struct containing `Model string` and `Stream *bool`; reject malformed JSON and blank models with wrapped errors. Implement `ProxyError.Error`, `StatusCode`, the exact `ErrorKind` constants `ERROR_INVALID_REQUEST`, `ERROR_UNKNOWN_MODEL`, `ERROR_UNSUPPORTED_FEATURE`, `ERROR_AUTH`, `ERROR_RATE_LIMIT`, `ERROR_UPSTREAM`, `ERROR_UNAVAILABLE`, `ERROR_TIMEOUT`, and `ERROR_PROTOCOL`, then encode Anthropic and OpenAI-shaped errors exactly as asserted above.

- [ ] **Step 4: Write failing multiline SSE and unexpected EOF tests**

```go
func TestSSEDecoderMultilineData(t *testing.T) {
	raw := "event: response.output_text.delta\r\ndata: {\"type\":\r\ndata: \"delta\"}\r\n\r\n"
	frame, err := NewSSEDecoder(strings.NewReader(raw)).Next()
	require.NoError(t, err)
	assert.Equal(t, "response.output_text.delta", frame.Event)
	assert.Equal(t, "{\"type\":\n\"delta\"}", string(frame.Data))
}

func TestSSEDecoderRejectsPartialFrameAtEOF(t *testing.T) {
	_, err := NewSSEDecoder(strings.NewReader("event: message_start\ndata: {}\n")).Next()
	require.ErrorIs(t, err, ErrUnexpectedEOF)
}

func TestWriteSSERoundTrip(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, WriteSSE(&out, SSEFrame{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)}))
	assert.Equal(t, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", out.String())
}
```

- [ ] **Step 5: Implement the full-frame SSE parser/writer**

Use `bufio.Reader.ReadString('\n')`, strip only CR/LF terminators, accumulate all `data:` lines with `\n`, preserve comment text beginning with `:`, parse `retry:` as non-negative integer milliseconds, and return a frame only after a blank line. Return `io.EOF` only when no frame bytes were read; return `ErrUnexpectedEOF` when EOF arrives after any recognized frame line but before the blank terminator. `WriteSSE` writes comments, optional `event`, `id`, and `retry`, one `data:` line per newline-delimited segment, and a final blank line; propagate every write error. Add a round-trip case containing comments, ID, retry, and multiline data.

- [ ] **Step 6: Run protocol tests and format files**

Run: `gofmt -w proxy/protocol/*.go && go test ./proxy/protocol -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the protocol foundation**

```bash
git add proxy/protocol
git commit -m "feat(proxy): add protocol envelopes and SSE framing"
```

### Task 2: Typed protocol DTO packages

**Files:**

- Create: `proxy/protocol/anthropic/types.go`
- Create: `proxy/protocol/anthropic/types_test.go`
- Create: `proxy/protocol/chat/types.go`
- Create: `proxy/protocol/chat/types_test.go`
- Create: `proxy/protocol/responses/types.go`
- Create: `proxy/protocol/responses/types_test.go`

**Interfaces:**

- Consumes: `protocol.Format` only in tests; DTO packages otherwise use stdlib JSON types.
- Produces: `anthropic.Request/Response`, `chat.Request/Response/StreamChunk`, and `responses.Request/Response/OutputItem` used by every transform task.

- [ ] **Step 1: Write DTO decode tests before moving types**

```go
func TestAnthropicMessageAcceptsStringOrBlocks(t *testing.T) {
	for _, raw := range []string{
		`{"role":"user","content":"hello"}`,
		`{"role":"user","content":[{"type":"text","text":"hello"}]}`,
	} {
		var msg Message
		require.NoError(t, json.Unmarshal([]byte(raw), &msg))
		require.Len(t, msg.Content, 1)
		assert.Equal(t, "text", msg.Content[0].Type)
		assert.Equal(t, "hello", msg.Content[0].Text)
	}
}

func TestResponsesRequestPreservesStatefulAndToolFields(t *testing.T) {
	raw := `{"model":"gpt-5","input":[{"role":"user","content":"hi"}],"previous_response_id":"resp_1","tools":[{"type":"web_search"}],"stream":true}`
	var req Request
	require.NoError(t, json.Unmarshal([]byte(raw), &req))
	assert.Equal(t, "resp_1", req.PreviousResponseID)
	require.Len(t, req.Tools, 1)
	assert.Equal(t, "web_search", req.Tools[0].Type)
	require.NotNil(t, req.Stream)
	assert.True(t, *req.Stream)
}
```

- [ ] **Step 2: Run DTO tests and confirm undefined types**

Run: `go test ./proxy/protocol/... -run 'TestAnthropicMessageAcceptsStringOrBlocks|TestResponsesRequestPreservesStatefulAndToolFields' -count=1`

Expected: FAIL with undefined DTO types.

- [ ] **Step 3: Move and normalize the existing DTOs**

Move the Anthropic DTOs currently in `proxy/adaptor/translator.go:16-119` into package `anthropic`, the Chat DTOs from `proxy/adaptor/translator.go:124-218` into package `chat`, and the Responses DTOs from `proxy/adaptor/translator.go:652-698` into package `responses`. Rename `CodexResponsePayload` to `responses.Request`/`responses.Response` as separate types; both may share `OutputItem`, `ContentBlock`, `Usage`, and `Tool`.

The Responses request must include these exact fields so later transforms can reject or preserve capabilities deliberately:

```go
type Request struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions,omitempty"`
	PreviousResponseID string          `json:"previous_response_id,omitempty"`
	Stream             *bool           `json:"stream,omitempty"`
	Store              *bool           `json:"store,omitempty"`
	Tools              []Tool          `json:"tools,omitempty"`
	ToolChoice         any             `json:"tool_choice,omitempty"`
	Reasoning          json.RawMessage `json:"reasoning,omitempty"`
}

type Response struct {
	ID     string       `json:"id,omitempty"`
	Model  string       `json:"model,omitempty"`
	Output []OutputItem `json:"output,omitempty"`
	Status string       `json:"status,omitempty"`
	Usage  *Usage       `json:"usage,omitempty"`
	Error  any          `json:"error,omitempty"`
}
```

Responses input accepts either a string or an item array. Add `DecodeInput(json.RawMessage) ([]InputItem, error)`; normalize a string into one user `input_text` item and preserve typed message, function-call, function-call-output, image, and file items. `DecodeRequest` calls `DecodeInput` so malformed scalars/arrays fail early, while `Encode` retains the API's accepted string/array shape.

Keep Anthropic `Message.UnmarshalJSON`, but return contextual errors for an invalid string or block array. Use `json.RawMessage` for tool argument/schema fields to avoid converting JSON numbers through `float64`.

- [ ] **Step 4: Add request validation helpers**

Each package exposes `DecodeRequest(raw []byte) (*Request, error)` and rejects a blank model. Anthropic additionally rejects `max_tokens < 0`; Chat and Responses reject malformed message/input arrays. Each package exposes `Encode(any) ([]byte, error)` as a thin `json.Marshal` wrapper so transform code never ignores marshal failures.

- [ ] **Step 5: Run all protocol tests**

Run: `gofmt -w proxy/protocol && go test ./proxy/protocol/... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the DTO packages**

```bash
git add proxy/protocol/anthropic proxy/protocol/chat proxy/protocol/responses
git commit -m "feat(proxy): add typed wire protocol DTOs"
```

### Task 3: Transform contracts, complete registry validation, and identity pairs

**Files:**

- Create: `proxy/transform/types.go`
- Create: `proxy/transform/registry.go`
- Create: `proxy/transform/registry_test.go`
- Create: `proxy/transform/identity.go`
- Create: `proxy/transform/identity_test.go`

**Interfaces:**

- Consumes: Task 1 envelopes/SSE and Task 2 DTO decode helpers.
- Produces: `Pair`, `RequestTransform`, `ResponseTransform`, `StreamTransform`, `NewRegistry`, `Registry.Lookup`, and three identity pair constructors.

- [ ] **Step 1: Write failing registry coverage tests**

```go
func noOpPair(from, to protocol.Format) Pair {
	return Pair{
		From: from,
		To: to,
		Request: func(_ context.Context, req protocol.RequestEnvelope) (protocol.TransformResult, error) {
			return protocol.TransformResult{Body: req.Body}, nil
		},
		Response: func(_ context.Context, resp protocol.ResponseEnvelope) (protocol.TransformResult, error) {
			return protocol.TransformResult{Body: resp.Body}, nil
		},
		NewStream: func(protocol.Exchange) (StreamTransform, error) { return identityStream{}, nil },
	}
}

func TestNewRegistryRequiresNineUniqueCompletePairs(t *testing.T) {
	var pairs []Pair
	for _, from := range protocol.ALL_FORMATS {
		for _, to := range protocol.ALL_FORMATS {
			pairs = append(pairs, noOpPair(from, to))
		}
	}
	reg, err := NewRegistry(pairs...)
	require.NoError(t, err)
	_, ok := reg.Lookup(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_RESPONSES)
	assert.True(t, ok)

	_, err = NewRegistry(pairs[:8]...)
	require.ErrorContains(t, err, "missing pair")
	_, err = NewRegistry(append(pairs, pairs[0])...)
	require.ErrorContains(t, err, "duplicate pair")
}
```

- [ ] **Step 2: Implement the transform contracts and registry**

```go
type RequestTransform func(context.Context, protocol.RequestEnvelope) (protocol.TransformResult, error)
type ResponseTransform func(context.Context, protocol.ResponseEnvelope) (protocol.TransformResult, error)
type StreamTransformFactory func(protocol.Exchange) (StreamTransform, error)

type StreamTransform interface {
	Push(context.Context, protocol.SSEFrame) ([]protocol.SSEFrame, error)
	Close(context.Context) ([]protocol.SSEFrame, error)
}

type Pair struct {
	From      protocol.Format
	To        protocol.Format
	Request   RequestTransform
	Response  ResponseTransform
	NewStream StreamTransformFactory
}

func newPair(from, to protocol.Format, request RequestTransform, response ResponseTransform, stream StreamTransformFactory) Pair {
	return Pair{From: from, To: to, Request: request, Response: response, NewStream: stream}
}
```

`NewRegistry` validates non-empty known formats, duplicate keys, nil functions, and the Cartesian product of `protocol.ALL_FORMATS`. It copies pairs into an unexported map so callers cannot mutate registrations after construction.

- [ ] **Step 3: Write failing identity tests**

```go
func TestIdentityPairsNormalizeModelWithoutSemanticChange(t *testing.T) {
	tests := []struct {
		name string
		pair Pair
		body string
		want string
	}{
		{"anthropic", AnthropicIdentity(), `{"model":"route/claude","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`, `{"model":"actual-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`},
		{"chat", ChatIdentity(), `{"model":"route/gpt","messages":[{"role":"user","content":"hi"}]}`, `{"model":"actual-model","messages":[{"role":"user","content":"hi"}]}`},
		{"responses", ResponsesIdentity(), `{"model":"route/gpt","input":"hi"}`, `{"model":"actual-model","input":"hi"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.pair.Request(context.Background(), protocol.RequestEnvelope{Model: "actual-model", Body: []byte(tc.body)})
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(result.Body))
		})
	}
}
```

Add a stream test proving identity forwards full `SSEFrame` values and that `Close` succeeds only after a source protocol terminal marker.

- [ ] **Step 4: Implement validated identity pairs**

Each identity request decodes with its Task 2 package, overwrites `Model` from the envelope, and re-encodes. Each identity response decodes and re-encodes a successful response. Identity stream validates JSON data frames, forwards the complete `SSEFrame`, and tracks success or failure terminals: Anthropic `message_stop`/`error`, Chat `[DONE]`, and Responses `response.completed`/`response.failed`. `Close` returns `protocol.ERROR_PROTOCOL` if no terminal marker arrived.

- [ ] **Step 5: Run transform foundation tests**

Run: `gofmt -w proxy/transform && go test ./proxy/transform -run 'TestNewRegistry|TestIdentity' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit registry and identity transforms**

```bash
git add proxy/transform
git commit -m "feat(proxy): add complete pairwise transform registry"
```

### Task 4: Anthropic Messages ↔ OpenAI Chat request transforms

**Files:**

- Create: `proxy/transform/anthropic_chat_request.go`
- Create: `proxy/transform/anthropic_chat_request_test.go`
- Create: `proxy/transform/helpers_test.go`
- Create: `proxy/transform/testdata/anthropic_request_full.json`
- Create: `proxy/transform/testdata/chat_from_anthropic.json`
- Create: `proxy/transform/testdata/chat_request_full.json`
- Create: `proxy/transform/testdata/anthropic_from_chat.json`

**Interfaces:**

- Consumes: `anthropic.Request`, `chat.Request`, `protocol.RequestEnvelope`, and `protocol.TransformResult`.
- Produces: `AnthropicToChatRequest` and `ChatToAnthropicRequest` with no HTTP/provider dependencies.

- [ ] **Step 1: Add full request fixtures and failing table tests**

Create the shared fixture loader in `proxy/transform/helpers_test.go`; later transform tasks extend this file rather than redefining helpers:

```go
func mustFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return body
}
```

The Anthropic fixture contains a system block, user text plus base64 image, assistant text plus `tool_use`, matching `tool_result`, tool schema, forced tool choice, temperature/top-p, and enabled thinking. The expected Chat fixture must contain:

```json
{
  "model": "target-model",
  "messages": [
    {"role": "system", "content": "You are concise."},
    {"role": "user", "content": [
      {"type": "text", "text": "inspect"},
      {"type": "image_url", "image_url": {"url": "data:image/png;base64,aW1n"}}
    ]},
    {"role": "assistant", "content": "checking", "tool_calls": [
      {"id": "call_1", "type": "function", "function": {"name": "read", "arguments": "{\"path\":\"a.txt\"}"}}
    ]},
    {"role": "tool", "tool_call_id": "call_1", "name": "read", "content": "ok"}
  ],
  "tools": [{"type": "function", "function": {"name": "read", "description": "Read a file", "parameters": {"type": "object"}}}],
  "tool_choice": {"type": "function", "function": {"name": "read"}},
  "reasoning_effort": "medium",
  "stream": true
}
```

Test both directions with:

```go
func TestAnthropicChatRequestTransforms(t *testing.T) {
	tests := []struct {
		name string
		fn   RequestTransform
		in   string
		want string
	}{
		{"anthropic to chat", AnthropicToChatRequest, "anthropic_request_full.json", "chat_from_anthropic.json"},
		{"chat to anthropic", ChatToAnthropicRequest, "chat_request_full.json", "anthropic_from_chat.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := mustFixture(t, tc.in)
			want := mustFixture(t, tc.want)
			got, err := tc.fn(context.Background(), protocol.RequestEnvelope{Model: "target-model", Stream: true, Body: in})
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got.Body))
		})
	}
}
```

- [ ] **Step 2: Run the request tests and confirm missing transforms**

Run: `go test ./proxy/transform -run TestAnthropicChatRequestTransforms -count=1`

Expected: FAIL because both transform functions are undefined.

- [ ] **Step 3: Implement Anthropic → Chat mapping**

Start with this exact control flow:

```go
func AnthropicToChatRequest(_ context.Context, env protocol.RequestEnvelope) (protocol.TransformResult, error) {
	src, err := anthropic.DecodeRequest(env.Body)
	if err != nil {
		return protocol.TransformResult{}, invalidRequest("decode anthropic request", err)
	}
	dst := chat.Request{
		Model: env.Model, Stream: env.Stream, MaxTokens: src.MaxTokens,
		Temperature: src.Temperature, TopP: src.TopP,
	}
	appendAnthropicSystem(&dst, src.System)
	if err := appendAnthropicMessages(&dst, src.Messages); err != nil {
		return protocol.TransformResult{}, err
	}
	if err := mapAnthropicTools(&dst, src.Tools, src.ToolChoice); err != nil {
		return protocol.TransformResult{}, err
	}
	dst.ReasoningEffort = reasoningEffort(src.Thinking)
	body, err := chat.Encode(dst)
	if err != nil {
		return protocol.TransformResult{}, fmt.Errorf("encode chat request: %w", err)
	}
	return protocol.TransformResult{Body: body, Losses: thinkingLoss(src.Thinking)}, nil
}
```

Define the production error/loss helpers in `anthropic_chat_request.go`:

```go
func invalidRequest(operation string, err error) error {
	return &protocol.ProxyError{
		Kind: protocol.ERROR_INVALID_REQUEST, Status: http.StatusBadRequest,
		Code: "invalid_request", Message: operation + ": " + err.Error(), Cause: err,
	}
}

func thinkingLoss(value *anthropic.Thinking) []protocol.SemanticLoss {
	if value == nil || value.BudgetTokens == 0 {
		return nil
	}
	return []protocol.SemanticLoss{{
		Field: "thinking.budget_tokens",
		Reason: "OpenAI reasoning_effort preserves only a low/medium/high bucket",
	}}
}
```

Implement helpers in the same file with these exact rules:

| Anthropic | Chat |
|---|---|
| `system` string/block list | ordered leading `role=system` messages |
| text | string when text-only; `[]ContentPart` when multimodal |
| base64 image | `image_url` data URL |
| assistant `tool_use` | assistant `tool_calls` |
| `tool_result` | separate `role=tool` message |
| input schema | function parameters |
| `tool_choice:any` | `required` |
| `tool_choice:tool` | named function choice |
| thinking budget `<=1024`, `<4096`, `>=4096` | `low`, `medium`, `high` plus `SemanticLoss{Field:"thinking.budget_tokens"}` |

Return `unsupported_feature` for Anthropic content block types other than text/image/thinking/tool_use/tool_result; never drop them.

- [ ] **Step 4: Implement Chat → Anthropic mapping**

Preserve system/developer order by appending their text to an ordered Anthropic system block array. If any developer message is present, add `SemanticLoss{Field:"messages.role", Reason:"Anthropic system blocks do not preserve Chat developer priority"}`. Map string and multipart content, tool calls/results, tool definitions, and named/required/auto tool choices. Map `reasoning_effort` low/medium/high to budgets `1024/2048/4096` and add a semantic loss. Reject remote image URLs because the current Anthropic DTO supports base64 only; accept `data:<mime>;base64,<data>` URLs.

- [ ] **Step 5: Add explicit unsupported-feature tests**

```go
func TestChatToAnthropicRejectsRemoteImage(t *testing.T) {
	body := []byte(`{"model":"gpt","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}]}`)
	_, err := ChatToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_UNSUPPORTED_FEATURE, proxyErr.Kind)
}
```

- [ ] **Step 6: Run and commit the pair request transforms**

Run: `gofmt -w proxy/transform && go test ./proxy/transform -run 'TestAnthropicChatRequestTransforms|TestChatToAnthropicRejectsRemoteImage' -count=1`

Expected: PASS.

```bash
git add proxy/transform
git commit -m "feat(proxy): translate Anthropic and Chat requests"
```

### Task 5: Anthropic Messages ↔ OpenAI Responses request transforms

**Files:**

- Create: `proxy/transform/anthropic_responses_request.go`
- Create: `proxy/transform/anthropic_responses_request_test.go`
- Create: `proxy/transform/testdata/responses_from_anthropic.json`
- Create: `proxy/transform/testdata/responses_request_full.json`
- Create: `proxy/transform/testdata/anthropic_from_responses.json`

**Interfaces:**

- Consumes: Task 2 Anthropic/Responses DTOs and Task 4 shared image/tool helpers where the wire shapes are identical.
- Produces: `AnthropicToResponsesRequest` and `ResponsesToAnthropicRequest`.

- [ ] **Step 1: Write the failing two-direction fixture test**

```go
func TestAnthropicResponsesRequestTransforms(t *testing.T) {
	tests := []struct { name string; fn RequestTransform; in, want string }{
		{"anthropic to responses", AnthropicToResponsesRequest, "anthropic_request_full.json", "responses_from_anthropic.json"},
		{"responses to anthropic", ResponsesToAnthropicRequest, "responses_request_full.json", "anthropic_from_responses.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(context.Background(), protocol.RequestEnvelope{Model: "target-model", Stream: true, Body: mustFixture(t, tc.in)})
			require.NoError(t, err)
			assert.JSONEq(t, string(mustFixture(t, tc.want)), string(got.Body))
		})
	}
}
```

- [ ] **Step 2: Run the tests and confirm missing functions**

Run: `go test ./proxy/transform -run TestAnthropicResponsesRequestTransforms -count=1`

Expected: FAIL with undefined `AnthropicToResponsesRequest` and `ResponsesToAnthropicRequest`.

- [ ] **Step 3: Implement Anthropic → Responses**

Migrate the proven ordering from `proxy/adaptor/translator.go:701-810`, but return errors instead of ignoring invalid JSON. Use these exact mappings:

| Anthropic | Responses |
|---|---|
| system | `instructions` |
| user/assistant text | input message with `input_text`/`output_text` |
| image | `input_image.image_url` data URL |
| `tool_use` | top-level `function_call` input item |
| `tool_result` | top-level `function_call_output` input item |
| function tool | Responses function tool |
| stream | explicit bool pointer |
| thinking budget | `reasoning:{"effort":"low|medium|high"}` plus semantic loss |

Do not set `store`; provider normalization owns that field.

- [ ] **Step 4: Implement Responses → Anthropic with explicit state/tool rules**

Convert input messages, `function_call`, and `function_call_output` in original order. Convert `instructions` to Anthropic system blocks. Convert `input_image` data URLs to Anthropic base64 source. Reject every non-empty `previous_response_id`, returning `ERROR_UNSUPPORTED_FEATURE` with code `stateful_context_not_portable`, because the proxy cannot retrieve the referenced provider-side history. Accept function tools; reject built-in tools such as `web_search`, `x_search`, `code_interpreter`, and `mcp` with code `unsupported_tool` because Anthropic Messages cannot reproduce their provider-side execution.

- [ ] **Step 5: Add the stateful-context regression test**

```go
func TestResponsesToAnthropicRejectsPreviousResponseWithoutHistory(t *testing.T) {
	body := []byte(`{"model":"gpt","previous_response_id":"resp_1","input":[{"role":"user","content":"next"}]}`)
	_, err := ResponsesToAnthropicRequest(context.Background(), protocol.RequestEnvelope{Model: "claude", Body: body})
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, "stateful_context_not_portable", proxyErr.Code)
}
```

- [ ] **Step 6: Run and commit the Anthropic/Responses request transforms**

Run: `gofmt -w proxy/transform && go test ./proxy/transform -run 'TestAnthropicResponsesRequestTransforms|TestResponsesToAnthropicRejectsPreviousResponseWithoutHistory' -count=1`

Expected: PASS.

```bash
git add proxy/transform
git commit -m "feat(proxy): translate Anthropic and Responses requests"
```

### Task 6: OpenAI Chat ↔ OpenAI Responses request transforms

**Files:**

- Create: `proxy/transform/chat_responses_request.go`
- Create: `proxy/transform/chat_responses_request_test.go`
- Create: `proxy/transform/testdata/responses_from_chat.json`
- Create: `proxy/transform/testdata/chat_from_responses.json`

**Interfaces:**

- Consumes: Chat and Responses DTOs plus Task 4/5 validated helper behavior.
- Produces: `ChatToResponsesRequest` and `ResponsesToChatRequest`; request coverage is then 9/9 including identities.

- [ ] **Step 1: Write failing full-fixture tests**

```go
func TestChatResponsesRequestTransforms(t *testing.T) {
	tests := []struct { name string; fn RequestTransform; in, want string }{
		{"chat to responses", ChatToResponsesRequest, "chat_request_full.json", "responses_from_chat.json"},
		{"responses to chat", ResponsesToChatRequest, "responses_request_full.json", "chat_from_responses.json"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn(context.Background(), protocol.RequestEnvelope{Model: "target-model", Stream: true, Body: mustFixture(t, tc.in)})
			require.NoError(t, err)
			assert.JSONEq(t, string(mustFixture(t, tc.want)), string(got.Body))
		})
	}
}
```

- [ ] **Step 2: Implement Chat → Responses**

Map system/developer messages into one ordered `instructions` string separated by `\n`; add `SemanticLoss{Field:"messages.role"}` when developer priority is collapsed. Map user/assistant messages to Responses message items, assistant tool calls to `function_call`, tool messages to `function_call_output`, function tools directly, and `reasoning_effort` into `reasoning.effort`. Preserve `stream`; do not set `store`.

- [ ] **Step 3: Implement Responses → Chat**

Map instructions to a leading system message, message items to Chat messages, function calls to assistant tool calls, and function outputs to tool messages. Convert data-URL images to Chat `image_url` parts. Reject provider built-in tools and nonportable `previous_response_id` using the same typed errors as Task 5. Convert reasoning effort to `ReasoningEffort` without a loss because both formats use the same low/medium/high domain.

- [ ] **Step 4: Run all nine request cases and semantic-loss tests**

Run: `gofmt -w proxy/transform && go test ./proxy/transform -run 'Request|Identity' -count=1`

Expected: PASS, covering 3 identity and 6 cross-format request directions.

- [ ] **Step 5: Commit Chat/Responses request transforms**

```bash
git add proxy/transform
git commit -m "feat(proxy): translate Chat and Responses requests"
```

### Task 7: All cross-format non-stream responses and source-native errors

**Files:**

- Create: `proxy/transform/anthropic_chat_response.go`
- Create: `proxy/transform/anthropic_responses_response.go`
- Create: `proxy/transform/chat_responses_response.go`
- Create: `proxy/transform/response.go`
- Create: `proxy/transform/response_test.go`
- Create: `proxy/transform/testdata/anthropic_response_full.json`
- Create: `proxy/transform/testdata/chat_response_full.json`
- Create: `proxy/transform/testdata/responses_response_full.json`

**Interfaces:**

- Consumes: `ResponseEnvelope.Exchange.OriginalRequest.Model`, protocol DTOs, and typed proxy errors.
- Produces: six cross-format `ResponseTransform` functions returning `TransformResult`; combined with identity responses this completes 9/9 non-stream paths and exposes response-side warnings/losses to the observer.

- [ ] **Step 1: Write a six-direction failing response matrix**

Extend `helpers_test.go` with the shared semantic model and deterministic response envelope:

```go
type responseSemantics struct {
	Text, Reasoning, ToolName, CallID string
	InputTokens, OutputTokens         int
	Stop                              string
}

func responseEnvelope(t *testing.T, fixture string, clientFormat protocol.Format) protocol.ResponseEnvelope {
	t.Helper()
	providerFormat := map[string]protocol.Format{
		"anthropic_response_full.json": protocol.FORMAT_ANTHROPIC_MESSAGES,
		"chat_response_full.json":      protocol.FORMAT_OPENAI_CHAT,
		"responses_response_full.json": protocol.FORMAT_OPENAI_RESPONSES,
	}[fixture]
	require.NotEmpty(t, providerFormat)
	return protocol.ResponseEnvelope{
		Status: http.StatusOK,
		Body:   mustFixture(t, fixture),
		Exchange: protocol.Exchange{
			OriginalRequest: protocol.RequestEnvelope{SourceFormat: clientFormat, Model: "client-model"},
			TranslatedRequest: protocol.RequestEnvelope{TargetFormat: providerFormat, Model: "provider-model"},
			ProviderID: "test-provider",
		},
	}
}
```

```go
func TestNonStreamResponseMatrix(t *testing.T) {
	tests := []struct { name string; fn ResponseTransform; in string; wantFormat protocol.Format }{
		{"anthropic to chat", AnthropicToChatResponse, "anthropic_response_full.json", protocol.FORMAT_OPENAI_CHAT},
		{"chat to anthropic", ChatToAnthropicResponse, "chat_response_full.json", protocol.FORMAT_ANTHROPIC_MESSAGES},
		{"anthropic to responses", AnthropicToResponsesResponse, "anthropic_response_full.json", protocol.FORMAT_OPENAI_RESPONSES},
		{"responses to anthropic", ResponsesToAnthropicResponse, "responses_response_full.json", protocol.FORMAT_ANTHROPIC_MESSAGES},
		{"chat to responses", ChatToResponsesResponse, "chat_response_full.json", protocol.FORMAT_OPENAI_RESPONSES},
		{"responses to chat", ResponsesToChatResponse, "responses_response_full.json", protocol.FORMAT_OPENAI_CHAT},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(context.Background(), responseEnvelope(t, tc.in, tc.wantFormat))
			require.NoError(t, err)
			assertResponseSemantics(t, tc.wantFormat, result.Body, responseSemantics{
				Text: "done", Reasoning: "checked", ToolName: "read", CallID: "call_1",
				InputTokens: 10, OutputTokens: 4, Stop: "tool",
			})
		})
	}
}
```

Implement `assertResponseSemantics(t, format, body, want)` in `helpers_test.go`. Switch on `format`, decode the matching target DTO, normalize it into `responseSemantics`, and compare every field with `assert.Equal`; do not compare generated IDs or timestamps literally. The normalizer must read text, reasoning/thinking, the first tool name/call ID, input/output usage, and stop reason from each DTO, so missing mappings fail this matrix rather than being hidden by JSON snapshots. Add one fixture with Anthropic cache-creation/cache-read usage and assert the result reports `SemanticLoss{Field:"usage.cache_tokens"}` when the target exposes only one cached-token bucket.

- [ ] **Step 2: Run the response matrix and verify undefined functions**

Run: `go test ./proxy/transform -run TestNonStreamResponseMatrix -count=1`

Expected: FAIL with undefined response transforms.

- [ ] **Step 3: Implement Anthropic ↔ Chat responses**

Migrate the existing logic from `TranslateOpenAIToAnthropicResponse` and the reverse response assembly in `proxy/adaptor/adaptor.go:628-684`. Map text, thinking/reasoning, all tool calls, usage, and stop reasons with these exact domains:

| Anthropic | Chat |
|---|---|
| `end_turn` | `stop` |
| `max_tokens` | `length` |
| `tool_use` | `tool_calls` |

Return `ERROR_PROTOCOL` for empty Chat choices, malformed tool arguments, or Anthropic tool blocks with invalid input JSON.

- [ ] **Step 4: Implement Anthropic ↔ Responses and Chat ↔ Responses responses**

Migrate and harden `TranslateResponsesToAnthropicMessage`; never accept a nil source. Responses output item rules are: reasoning summary → thinking/reasoning, message output text → assistant text, function_call → tool call. For Chat ↔ Responses, use the same output item semantics and preserve response ID/model/status/usage. Map incomplete Responses status to `max_tokens`/`length`; presence of function calls takes precedence over normal stop.

- [ ] **Step 5: Add upstream error decode/encode tests**

```go
func TestEncodeUpstreamErrorForClientFormat(t *testing.T) {
	err := DecodeUpstreamError(429, http.Header{"Retry-After": {"7"}, "x-request-id": {"req_1"}}, []byte(`{"error":{"message":"slow"}}`))
	assert.Equal(t, protocol.ERROR_RATE_LIMIT, err.Kind)
	assert.Equal(t, 7*time.Second, err.RetryAfter)
	assert.Equal(t, "req_1", err.UpstreamRequestID)
	for _, format := range protocol.ALL_FORMATS {
		body, encodeErr := protocol.EncodeError(format, err)
		require.NoError(t, encodeErr)
		assert.NotContains(t, string(body), "Bearer")
	}
}
```

Implement `DecodeUpstreamError` in `proxy/transform/response.go` with this signature:

```go
func DecodeUpstreamError(status int, headers http.Header, body []byte) *protocol.ProxyError
```

Map `401/403` to `ERROR_AUTH`, `429` to `ERROR_RATE_LIMIT`, `408/504` to `ERROR_TIMEOUT`, `502/503` to `ERROR_UNAVAILABLE`, and other statuses to `ERROR_UPSTREAM`. Parse `Retry-After` as either integer seconds or an HTTP date. Accept request IDs only from `x-request-id`, `request-id`, and `cf-ray`. Decode only JSON fields `error.message`, `error.code`, `message`, and `code`; cap the selected message at 512 UTF-8 bytes. For non-JSON/HTML bodies use `"upstream request failed"`, never raw body text or arbitrary headers.

- [ ] **Step 6: Run all non-stream tests and commit**

Run: `gofmt -w proxy/transform && go test ./proxy/transform -run 'TestNonStreamResponseMatrix|TestEncodeUpstreamError' -count=1`

Expected: PASS.

```bash
git add proxy/transform
git commit -m "feat(proxy): translate all non-stream responses"
```

### Task 8: Anthropic Messages ↔ OpenAI Chat streaming state machines

**Files:**

- Create: `proxy/transform/anthropic_chat_stream.go`
- Create: `proxy/transform/anthropic_chat_stream_test.go`
- Modify: `proxy/transform/helpers_test.go`

**Interfaces:**

- Consumes: Task 1 `SSEFrame`, Task 2 stream DTOs, and Task 7 stop/error mappings.
- Produces: `NewAnthropicToChatStream` and `NewChatToAnthropicStream`.

- [ ] **Step 1: Write the failing Chat → Anthropic lifecycle test**

Extend `helpers_test.go` with the shared stream helpers:

```go
func exchangeFor(originalModel, translatedModel string) protocol.Exchange {
	next := 0
	return protocol.Exchange{
		OriginalRequest: protocol.RequestEnvelope{Model: originalModel},
		TranslatedRequest: protocol.RequestEnvelope{Model: translatedModel},
		ProviderID: "test-provider",
		NewID: func() string {
			next++
			return fmt.Sprintf("generated_%d", next)
		},
	}
}

func pushAll(t *testing.T, stream StreamTransform, input []protocol.SSEFrame) []protocol.SSEFrame {
	t.Helper()
	var output []protocol.SSEFrame
	for _, frame := range input {
		frames, err := stream.Push(context.Background(), frame)
		require.NoError(t, err)
		output = append(output, frames...)
	}
	return output
}

func closeStream(t *testing.T, stream StreamTransform, output *[]protocol.SSEFrame) error {
	t.Helper()
	frames, err := stream.Close(context.Background())
	if output != nil {
		*output = append(*output, frames...)
	}
	return err
}

func eventNames(frames []protocol.SSEFrame) []string {
	names := make([]string, 0, len(frames))
	for _, frame := range frames {
		names = append(names, frame.Event)
	}
	return names
}
```

```go
func fullChatFrames() []protocol.SSEFrame {
	return []protocol.SSEFrame{
		{Data: []byte(`{"id":"chat_1","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"reasoning_content":"check"}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"done"}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":"}}]}}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)},
		{Data: []byte(`[DONE]`)},
	}
}

func TestChatToAnthropicStreamLifecycle(t *testing.T) {
	stream, err := NewChatToAnthropicStream(exchangeFor("gpt-4o", "claude-3"))
	require.NoError(t, err)
	output := pushAll(t, stream, fullChatFrames())
	require.NoError(t, closeStream(t, stream, &output))
	assert.Equal(t, []string{
		"message_start", "content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_stop",
		"content_block_start", "content_block_delta", "content_block_delta", "content_block_stop",
		"message_delta", "message_stop",
	}, eventNames(output))
}
```

- [ ] **Step 2: Implement Chat → Anthropic state**

Use a struct with `messageID`, `model`, `started`, `terminal`, `nextBlock`, optional thinking/text block indices, `map[int]*chatToolState`, usage, and stop reason. `chatToolState` stores downstream block index, call ID, name, and argument buffer. Emit `message_start` once, close thinking before text, close text before tool blocks, keep tool indices stable across partial deltas, validate accumulated argument JSON when the tool finishes, and emit `message_delta` then `message_stop` once. `[DONE]` without a prior finish reason is `ERROR_PROTOCOL`.

- [ ] **Step 3: Write the failing Anthropic → Chat lifecycle test**

Feed `message_start`, thinking block start/delta/stop, text start/delta/stop, tool start/partial-json/stop, `message_delta(tool_use)`, and `message_stop`. Assert the output starts with a Chat assistant-role chunk, carries `reasoning_content`, text, indexed tool call deltas, a `finish_reason:"tool_calls"` chunk, and one `[DONE]` frame.

- [ ] **Step 4: Implement Anthropic → Chat state**

Track Anthropic content block index → Chat tool index, call ID, and name. Convert `thinking_delta`, `text_delta`, and `input_json_delta`. The first output is always an assistant role primer. Convert `message_delta.stop_reason` through Task 7 mappings. Treat an Anthropic `error` event as a Chat `data:{"error":...}` frame followed by `[DONE]`, and mark the stream terminal.

- [ ] **Step 5: Test unexpected EOF and state isolation**

```go
func TestChatToAnthropicCloseRejectsMissingTerminal(t *testing.T) {
	stream, _ := NewChatToAnthropicStream(exchangeFor("gpt", "claude"))
	_, err := stream.Push(context.Background(), protocol.SSEFrame{Data: []byte(`{"choices":[{"delta":{"content":"partial"}}]}`)})
	require.NoError(t, err)
	err = closeStream(t, stream, nil)
	var proxyErr *protocol.ProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, protocol.ERROR_PROTOCOL, proxyErr.Kind)
}
```

Create two streams concurrently with different call IDs and assert neither output contains the other's ID. Run the test with `-race`.

- [ ] **Step 6: Run and commit Anthropic/Chat stream transforms**

Run: `gofmt -w proxy/transform && go test -race ./proxy/transform -run 'TestChatToAnthropicStream|TestAnthropicToChatStream' -count=1`

Expected: PASS.

```bash
git add proxy/transform
git commit -m "feat(proxy): stream Anthropic and Chat responses"
```

### Task 9: Anthropic Messages ↔ OpenAI Responses streaming state machines

**Files:**

- Create: `proxy/transform/anthropic_responses_stream.go`
- Create: `proxy/transform/anthropic_responses_stream_test.go`
- Modify: `proxy/transform/helpers_test.go`

**Interfaces:**

- Consumes: Task 8 stream helper/testing conventions and Responses SSE event names.
- Produces: `NewAnthropicToResponsesStream` and `NewResponsesToAnthropicStream`.

- [ ] **Step 1: Write a failing Responses → Anthropic full lifecycle test**

Define `fullResponsesFrames()` in `anthropic_responses_stream_test.go` to return this exact Responses event order. Use the function in this lifecycle test and reuse it in Task 10:

```go
func fullResponsesFrames() []protocol.SSEFrame {
	return []protocol.SSEFrame{
	{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`)},
	{Event: "response.reasoning_summary_text.delta", Data: []byte(`{"type":"response.reasoning_summary_text.delta","delta":"check"}`)},
	{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","content":[]}}`)},
	{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","delta":"done"}`)},
	{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"read","arguments":""}}`)},
	{Event: "response.function_call_arguments.delta", Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"path\":\"a.txt\"}"}`)},
	{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":4}}}`)},
	}
}
```

Assert exact Anthropic lifecycle order, stable `call_1`, valid partial JSON delta, stop reason `tool_use`, and input/output usage.

- [ ] **Step 2: Implement Responses → Anthropic state**

Migrate the state concepts from `proxy/adaptor/adaptor.go:853-950` and event handling from `1017-1205`, but replace maps of `any` with typed event structs or narrow local decode structs, return every JSON error, sort/close open blocks by numeric block index, and mark terminal on `response.completed` or `response.failed`. Map `item_id` to `call_id`; validate function arguments at item completion. `response.failed` emits one Anthropic `error` event and no `message_stop` success event.

- [ ] **Step 3: Write the failing Anthropic → Responses lifecycle test**

Feed a complete Anthropic stream with thinking, text, tool use, usage, and stop reason. Assert Responses events include `response.created`, `response.in_progress`, reasoning summary events, message output item/content part start/delta/done events, function call item plus argument delta/done, and exactly one `response.completed` containing output and usage.

- [ ] **Step 4: Implement Anthropic → Responses state**

Read IDs from `Exchange.NewID`; reject a nil generator in each stream constructor. Tests use `exchangeFor`, while `Handler` injects `uuid.NewString`. Accumulate final `Response.Output` while emitting deltas so `response.completed` contains the same text, reasoning summary, function calls, status, and usage as the non-stream response from Task 7.

- [ ] **Step 5: Add stream-equivalence and failed-event tests**

Fold the emitted events into a `responses.Response`, compare its semantic fields with `AnthropicToResponsesResponse` for the equivalent non-stream fixture, and assert an Anthropic `event:error` becomes `response.failed` with no `response.completed`.

- [ ] **Step 6: Run and commit Anthropic/Responses streams**

Run: `gofmt -w proxy/transform && go test -race ./proxy/transform -run 'TestResponsesToAnthropicStream|TestAnthropicToResponsesStream' -count=1`

Expected: PASS.

```bash
git add proxy/transform
git commit -m "feat(proxy): stream Anthropic and Responses events"
```

### Task 10: OpenAI Chat ↔ Responses streams and production registry assembly

**Files:**

- Create: `proxy/transform/chat_responses_stream.go`
- Create: `proxy/transform/chat_responses_stream_test.go`
- Create: `proxy/transform/collector.go`
- Create: `proxy/transform/collector_test.go`
- Create: `proxy/transform/default.go`
- Create: `proxy/transform/default_test.go`

**Interfaces:**

- Consumes: all request/response/stream functions from Tasks 3–9.
- Produces: `NewChatToResponsesStream`, `NewResponsesToChatStream`, source-format stream collectors, six cross-format pair constructors, and `NewDefaultRegistry`.

- [ ] **Step 1: Write failing Chat ↔ Responses lifecycle tests**

For Chat → Responses, feed the Chat frames from Task 8 and assert role/content/tool deltas become Responses created/in-progress/output-item/content-part/function-argument/completed events. For Responses → Chat, feed Task 9 Responses frames and assert one role primer, reasoning/content/tool deltas, finish reason `tool_calls`, and one `[DONE]`.

```go
func TestChatResponsesStreamEquivalence(t *testing.T) {
	chatToResponses, _ := NewChatToResponsesStream(exchangeFor("gpt-chat", "gpt-responses"))
	responsesFrames := pushAll(t, chatToResponses, fullChatFrames())
	require.NoError(t, closeStream(t, chatToResponses, &responsesFrames))
	assertResponsesSemantics(t, responsesFrames, responseSemantics{Text: "done", ToolName: "read", CallID: "call_1"})

	responsesToChat, _ := NewResponsesToChatStream(exchangeFor("gpt-responses", "gpt-chat"))
	chatFrames := pushAll(t, responsesToChat, fullResponsesFrames())
	require.NoError(t, closeStream(t, responsesToChat, &chatFrames))
	assertChatSemantics(t, chatFrames, responseSemantics{Text: "done", ToolName: "read", CallID: "call_1"})
}
```

Implement the assertion helpers in `chat_responses_stream_test.go` with these exact signatures:

```go
func assertResponsesSemantics(t *testing.T, frames []protocol.SSEFrame, want responseSemantics)
func assertChatSemantics(t *testing.T, frames []protocol.SSEFrame, want responseSemantics)
```

Both helpers call the production stream collector below, then pass its JSON body to `assertResponseSemantics`. They compare `Text`, `Reasoning`, `ToolName`, and `CallID` individually and fail on malformed JSON, duplicate terminal events, or a missing terminal event.

- [ ] **Step 2: Implement Chat → Responses state**

Reuse only small pure helpers such as stop-reason mapping; do not route through Anthropic as an intermediate format. Track Chat choice/tool indices directly and build Responses message/function output items. Emit a completed response whose folded semantics equal `ChatToResponsesResponse`.

- [ ] **Step 3: Implement Responses → Chat state**

Track Responses item IDs and tool call indices directly. Emit one Chat assistant role primer. Map reasoning summary to `reasoning_content`, output text to `content`, and function calls to indexed `tool_calls`. Convert completed/incomplete status and function-call presence to finish reason, then emit `[DONE]`. Convert `response.failed` to the specified Chat error data frame plus `[DONE]`.

- [ ] **Step 4: Promote stream folding into production collectors**

Define:

```go
type StreamCollector interface {
	Push(context.Context, protocol.SSEFrame) error
	Close(context.Context) (protocol.TransformResult, error)
}

func NewStreamCollector(format protocol.Format, exchange protocol.Exchange) (StreamCollector, error)
```

Implement one stateful collector per source format. Anthropic collector rebuilds `anthropic.Response` from message/content/delta/usage events; Chat collector rebuilds `chat.Response` from chunks until `[DONE]`; Responses collector returns the complete response carried by `response.completed`. All collectors preserve text, reasoning, stable tool IDs/arguments, usage, model, and stop reason; reject malformed JSON, invalid tool arguments, failure events, duplicate terminals, and `Close` without a terminal. Add a three-format table test using the complete stream fixtures and compare each collected body through `assertResponseSemantics` against the corresponding non-stream fixture.

- [ ] **Step 5: Assemble all nine production pairs explicitly**

```go
func NewDefaultRegistry() (*Registry, error) {
	return NewRegistry(
		AnthropicIdentity(),
		newPair(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_CHAT, AnthropicToChatRequest, ChatToAnthropicResponse, NewChatToAnthropicStream),
		newPair(protocol.FORMAT_ANTHROPIC_MESSAGES, protocol.FORMAT_OPENAI_RESPONSES, AnthropicToResponsesRequest, ResponsesToAnthropicResponse, NewResponsesToAnthropicStream),
		newPair(protocol.FORMAT_OPENAI_CHAT, protocol.FORMAT_ANTHROPIC_MESSAGES, ChatToAnthropicRequest, AnthropicToChatResponse, NewAnthropicToChatStream),
		ChatIdentity(),
		newPair(protocol.FORMAT_OPENAI_CHAT, protocol.FORMAT_OPENAI_RESPONSES, ChatToResponsesRequest, ResponsesToChatResponse, NewResponsesToChatStream),
		newPair(protocol.FORMAT_OPENAI_RESPONSES, protocol.FORMAT_ANTHROPIC_MESSAGES, ResponsesToAnthropicRequest, AnthropicToResponsesResponse, NewAnthropicToResponsesStream),
		newPair(protocol.FORMAT_OPENAI_RESPONSES, protocol.FORMAT_OPENAI_CHAT, ResponsesToChatRequest, ChatToResponsesResponse, NewChatToResponsesStream),
		ResponsesIdentity(),
	)
}
```

- [ ] **Step 6: Verify exact 9/9 request, non-stream, and stream coverage**

```go
func TestDefaultRegistryCoversMatrix(t *testing.T) {
	reg, err := NewDefaultRegistry()
	require.NoError(t, err)
	for _, from := range protocol.ALL_FORMATS {
		for _, to := range protocol.ALL_FORMATS {
			pair, ok := reg.Lookup(from, to)
			require.Truef(t, ok, "%s -> %s", from, to)
			require.NotNil(t, pair.Request)
			require.NotNil(t, pair.Response)
			require.NotNil(t, pair.NewStream)
		}
	}
}
```

Run: `gofmt -w proxy/transform && go test -race ./proxy/transform -count=1`

Expected: PASS; all 27 fundamental transform paths are registered and tested.

- [ ] **Step 7: Commit the final protocol matrix**

```bash
git add proxy/transform
git commit -m "feat(proxy): complete the 3x3 transform matrix"
```

### Task 11: Provider-family routing and concrete provider profiles

**Files:**

- Create: `proxy/route/profile.go`
- Create: `proxy/route/router.go`
- Create: `proxy/route/router_test.go`
- Create: `proxy/upstream/profile.go`
- Create: `proxy/upstream/profile_test.go`

**Interfaces:**

- Consumes: `protocol.Format` and no credential I/O yet.
- Produces: `route.Profile`, `route.Route`, `route.Router.Resolve`, `upstream.Profile`, `upstream.Catalog`, `DefaultCatalog`, `Catalog.ResolveProfile`, endpoint/auth/normalizer metadata.

- [ ] **Step 1: Write failing deterministic routing tests**

```go
func TestRouterResolve(t *testing.T) {
	router, err := NewRouter([]Profile{
		{ID: "anthropic", Qualifiers: []string{"anthropic"}, Prefixes: []string{"claude-"}},
		{ID: "openai", Qualifiers: []string{"openai", "openai-chat"}, Prefixes: []string{"gpt-", "o1-", "o3-"}},
		{ID: "xai", Qualifiers: []string{"xai", "xai-chat"}, Prefixes: []string{"grok-"}},
		{ID: "minimax", Qualifiers: []string{"minimax"}, Prefixes: []string{"minimax-"}},
	})
	require.NoError(t, err)
	tests := []struct {
		model string
		wantProvider string
		wantModel string
		wantForced *protocol.Format
		wantErr bool
	}{
		{"xai/grok-4.5", "xai", "grok-4.5", nil, false},
		{"xai-chat/grok-4.5", "xai", "grok-4.5", formatPtr(protocol.FORMAT_OPENAI_CHAT), false},
		{"gpt-5", "openai", "gpt-5", nil, false},
		{"openai-chat/gpt-5", "openai", "gpt-5", formatPtr(protocol.FORMAT_OPENAI_CHAT), false},
		{"claude-3-5-sonnet-latest", "anthropic", "claude-3-5-sonnet-latest", nil, false},
		{"unknown-model", "", "", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			got, err := router.Resolve(protocol.FORMAT_OPENAI_CHAT, tc.model)
			if tc.wantErr {
				var proxyErr *protocol.ProxyError
				require.ErrorAs(t, err, &proxyErr)
				assert.Equal(t, protocol.ERROR_UNKNOWN_MODEL, proxyErr.Kind)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantProvider, got.ProviderID)
			assert.Equal(t, tc.wantModel, got.Model)
			assert.Equal(t, tc.wantForced, got.ForcedTarget)
		})
	}
}

func formatPtr(value protocol.Format) *protocol.Format { return &value }
```

- [ ] **Step 2: Implement route profiles and router**

```go
type Profile struct {
	ID         string
	Qualifiers []string
	ExactModels []string
	Prefixes   []string
}

type Route struct {
	ProviderID  string
	Model       string
	SourceFormat protocol.Format
	ForcedTarget *protocol.Format
}
```

`NewRouter` rejects duplicate profile IDs/qualifiers and empty prefixes. Normalize IDs, qualifiers, exact-model keys, and prefix keys with `strings.ToLower` for matching and duplicate detection, but preserve the caller's model spelling in `Route.Model`. `Resolve` checks qualified `<qualifier>/<model>` first, then exact model membership, then anchored prefix. It returns an error for zero or multiple matches. Qualifiers ending in `-chat` force `FORMAT_OPENAI_CHAT`; all other qualifiers leave target selection to the concrete profile. Add `MiniMax-Text-01` as a case-insensitive unqualified regression case.

- [ ] **Step 3: Write failing concrete profile tests**

```go
func TestDefaultCatalogCapabilities(t *testing.T) {
	catalog, err := DefaultCatalog()
	require.NoError(t, err)
	tests := []struct {
		id string
		preferred protocol.Format
		endpoints map[protocol.Format]string
	}{
		{"anthropic", protocol.FORMAT_ANTHROPIC_MESSAGES, map[protocol.Format]string{protocol.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"}},
		{"minimax", protocol.FORMAT_ANTHROPIC_MESSAGES, map[protocol.Format]string{protocol.FORMAT_ANTHROPIC_MESSAGES: "/v1/messages"}},
		{"openai-api", protocol.FORMAT_OPENAI_RESPONSES, map[protocol.Format]string{protocol.FORMAT_OPENAI_RESPONSES: "/v1/responses", protocol.FORMAT_OPENAI_CHAT: "/v1/chat/completions"}},
		{"openai-codex-oauth", protocol.FORMAT_OPENAI_RESPONSES, map[protocol.Format]string{protocol.FORMAT_OPENAI_RESPONSES: "/codex/responses"}},
		{"xai", protocol.FORMAT_OPENAI_RESPONSES, map[protocol.Format]string{protocol.FORMAT_OPENAI_RESPONSES: "/v1/responses", protocol.FORMAT_OPENAI_CHAT: "/v1/chat/completions"}},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			profile, ok := catalog.Lookup(tc.id)
			require.True(t, ok)
			assert.Equal(t, tc.preferred, profile.Preferred)
			assert.Equal(t, tc.endpoints, profile.Endpoints)
		})
	}
}
```

- [ ] **Step 4: Implement concrete profiles and provider normalization**

`upstream.Profile` embeds routing family metadata and adds `CredentialProvider`, `BaseURL`, `Endpoints`, `Preferred`, `AuthScheme`, `AllowedRequestHeaders`, `AllowedResponseHeaders`, `AdvertisedModels`, and `NormalizeRequest`. Define auth schemes `AUTH_X_API_KEY` and `AUTH_BEARER`. The default request allowlist is `x-request-id`, `traceparent`, and `tracestate`; Anthropic additionally permits `anthropic-beta` but overwrites `anthropic-version` with the profile value. The response allowlist is `content-type`, `retry-after`, `x-request-id`, `request-id`, and `cf-ray`. Matching is case-insensitive. `Authorization`, `x-api-key`, `cookie`, `set-cookie`, `host`, connection headers, and all `X-Forwarded-*` headers are always rejected even if a future profile accidentally lists them.

Provider normalizers perform only these mutations:

- Anthropic: preserve body and add header metadata later.
- MiniMax: preserve Anthropic wire body.
- OpenAI API: preserve selected Chat/Responses body.
- Codex OAuth: Responses upstream always uses `stream=true`, `store=false`, and a present `instructions` string (empty when absent). When the client requested non-stream, mark the normalized request for the handler's stream-to-JSON bridge; do not expose the forced upstream stream mode to the client.
- xAI: preserve Responses/Chat body and reject tools unsupported by the selected target format instead of deleting them.

Define the normalizer result explicitly:

```go
type NormalizedRequest struct {
	Body              []byte
	UpstreamStream    bool
	BridgeToNonStream bool
}

type NormalizeRequest func(protocol.RequestEnvelope) (NormalizedRequest, error)
```

For ordinary profiles, `UpstreamStream` equals the client envelope's `Stream` and `BridgeToNonStream` is false. Codex sets `UpstreamStream=true` and `BridgeToNonStream=!env.Stream`, updating the serialized `stream` field consistently. `Catalog.ResolveProfile(providerFamily, credentialKind, forcedTarget)` selects `openai-codex-oauth` for OpenAI OAuth and `openai-api` for OpenAI API keys; other families select their single concrete profile. Reject forced Chat for Codex OAuth. Verify selected target is supported before returning it.

Add `TestCodexNormalizerMarksNonStreamBridge`: normalize `{"model":"gpt-5","input":"hi","stream":false}` and assert the body contains `stream:true`, `store:false`, `instructions:""`, `UpstreamStream` is true, and `BridgeToNonStream` is true. A second streaming case must keep `BridgeToNonStream` false.

Use these production URI/auth defaults, overridden only by a non-empty validated `Credential.BaseURL`:

| Profile | Base URL | Default endpoint | Auth |
|---|---|---|---|
| `anthropic` | `https://api.anthropic.com` | `/v1/messages` | API key → `x-api-key`; OAuth → Bearer |
| `minimax` | `https://api.minimax.io/anthropic` | `/v1/messages` | `x-api-key` |
| `openai-api` | `https://api.openai.com` | `/v1/responses` | Bearer |
| `openai-codex-oauth` | `https://chatgpt.com/backend-api` | `/codex/responses` | Bearer + optional `ChatGPT-Account-ID` |
| `xai` | `https://api.x.ai` | `/v1/responses` | Bearer |

xAI's preferred payload is the standard Responses request from Task 2 (`model`, `input`, optional `instructions`, `tools`, `tool_choice`, `reasoning`, `stream`); forced `xai-chat/...` uses the standard Chat request and `/v1/chat/completions` instead.

- [ ] **Step 5: Run and commit routing/profile tests**

Run: `gofmt -w proxy/route proxy/upstream && go test ./proxy/route ./proxy/upstream -count=1`

Expected: PASS.

```bash
git add proxy/route proxy/upstream/profile.go proxy/upstream/profile_test.go
git commit -m "feat(proxy): route models through provider profiles"
```

### Task 12: Credential resolution and safe upstream HTTP client

**Files:**

- Create: `proxy/upstream/credential.go`
- Create: `proxy/upstream/credential_test.go`
- Create: `proxy/upstream/client.go`
- Create: `proxy/upstream/client_test.go`

**Interfaces:**

- Consumes: `auth.Credential`, `auth.Authenticator`, concrete profiles from Task 11, and `config.ProxyTimeoutConfig`.
- Produces: `CredentialResolver.Resolve`, `ResolvedCredential`, `Client.Do`, `Client.CountTokens`, and request/header/timeout guarantees used by the handler.

- [ ] **Step 1: Write failing active/env/refresh credential tests**

```go
type credentialStore interface {
	Dir() string
	Load(string) (*auth.Credential, error)
	List() ([]*auth.Credential, error)
	Save(*auth.Credential) error
}

func TestCredentialResolverRefreshesWithRequestContextAndSaves(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeCredentialStore{creds: []*auth.Credential{{
		Provider: "openai", Kind: auth.KIND_OAUTH, AccessToken: "old", RefreshToken: "refresh",
		ExpiresAt: time.Now().Add(-time.Minute),
	}}}
	authenticator := &fakeAuthenticator{refresh: &auth.Credential{
		Provider: "openai", Kind: auth.KIND_OAUTH, AccessToken: "new", RefreshToken: "rotated",
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	resolver := NewCredentialResolver(store, func(*auth.Credential) (auth.Authenticator, error) { return authenticator, nil }, os.LookupEnv)
	cred, err := resolver.Resolve(ctx, "openai")
	require.NoError(t, err)
	assert.Equal(t, "new", cred.AccessToken)
	assert.Same(t, ctx, authenticator.refreshContext)
	require.Len(t, store.saved, 1)
	assert.Equal(t, "rotated", store.saved[0].RefreshToken)
}
```

Add tests for active.json selection, alphabetic fallback, `ANTHROPIC_API_KEY`/`OPENAI_API_KEY`/`XAI_API_KEY`/`MINIMAX_API_KEY` fallback, refresh failure, and save failure. Save failure must return `ERROR_UNAVAILABLE`; the rotated token must not be used.

- [ ] **Step 2: Implement credential resolution**

Read `active.json` directly from `filepath.Join(store.Dir(), "active.json")` as `map[string]string`, where each provider family maps to an `auth.Credential.Name()`; a missing file means no active selection, while malformed JSON, an unloadable active name, or a credential whose `Provider` differs from the requested family is an error. Resolve active credential first, then first sorted matching provider credential, then environment API key. Use `auth.DEFAULT_EXPIRY_SKEW`; on expiry call the injected `provider.For` equivalent with the request context, require successful refresh and save, then return the saved credential. MiniMax env credentials use provider `minimax` and kind `KIND_API_KEY` even though login is not part of the auth registry.

- [ ] **Step 3: Write failing upstream endpoint/header/body tests**

```go
func TestClientDoUsesProfileEndpointAndSanitizedHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/responses", r.URL.Path)
		assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("X-Forwarded-Authorization"))
		assert.Equal(t, "req_client", r.Header.Get("x-request-id"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"model":"grok-4.5","input":"hi"}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed"}`))
	}))
	defer server.Close()

	profile := testXAIProfile(server.URL)
	client, err := NewClient(server.Client(), timeoutConfig())
	require.NoError(t, err)
	resp, err := client.Do(context.Background(), profile, &auth.Credential{Provider: "xai", Kind: auth.KIND_API_KEY, APIKey: "secret"}, protocol.RequestEnvelope{
		TargetFormat: protocol.FORMAT_OPENAI_RESPONSES,
		Headers: http.Header{"x-request-id": {"req_client"}, "Authorization": {"Bearer client-secret"}},
		Body: []byte(`{"model":"grok-4.5","input":"hi"}`),
	})
	require.NoError(t, err)
	defer resp.Body.Close()
}

func testXAIProfile(baseURL string) Profile {
	catalog, err := DefaultCatalog()
	if err != nil {
		panic(err)
	}
	profile, ok := catalog.Lookup("xai")
	if !ok {
		panic("default xAI profile missing")
	}
	profile.BaseURL = baseURL
	return profile
}

func timeoutConfig() config.ProxyTimeoutConfig {
	return config.ProxyTimeoutConfig{
		MessagesMs: 2000, StreamMessagesMs: 5000, CountTokensMs: 2000,
	}
}
```

- [ ] **Step 4: Implement the injected upstream client**

`NewClient(httpClient *http.Client, cfg config.ProxyTimeoutConfig)` returns `(*Client, error)` and rejects nil clients or non-positive timeout values. `Do` resolves the path from the profile and target format, then uses `Credential.BaseURL` when non-empty or the profile default otherwise. Validate the base URL as absolute, with `https` scheme, non-empty host, and no userinfo/query/fragment; permit `http` only for loopback hosts so local gateways and `httptest.Server` remain usable. Join the fixed endpoint without allowing the base path to erase it, create a context-bound POST request, set `Content-Type`, `Accept` for streams, provider auth, and only allowlisted downstream headers. It never logs the body. Wrap transport errors as typed timeout (`504`) or upstream (`502`) errors.

Apply auth exactly:

- Anthropic API key and MiniMax: `x-api-key`.
- Anthropic OAuth: `Authorization: Bearer`, `anthropic-dangerous-direct-browser-access:true`, and ensure `anthropic-beta` contains `oauth-2025-04-20` without duplicating existing beta values.
- OpenAI API/Codex/xAI: `Authorization: Bearer`.
- Codex: include `ChatGPT-Account-ID` only when `Credential.AccountID` is non-empty; set `originator:codex_cli_rs`, `version:0.125.0`, and a `User-Agent` derived from those constants plus `runtime.GOOS/GOARCH`, matching the checked-in `tmp/auth2api` contract.
- Anthropic: set `anthropic-version: 2023-06-01`.

Keep these as `ANTHROPIC_OAUTH_BETA`, `DEFAULT_CODEX_ORIGINATOR`, and `DEFAULT_CODEX_VERSION` beside the profile definitions and cover their exact values in header tests; do not scatter literals through handlers.

Clone the injected `http.Client` so caller state is not mutated, force the clone's `Timeout` to zero, and when its transport is `*http.Transport`, clone that transport and set `ResponseHeaderTimeout` to `MessagesMs`. Use `MessagesMs` for the non-stream request context and `StreamMessagesMs` for the streaming context lifetime, always deriving from the caller context. Wrap `resp.Body` so `Close` releases the derived timeout context. This preserves caller cancellation and prevents an independent `http.Client.Timeout` from aborting long SSE bodies.

- [ ] **Step 5: Implement native token counting capability**

Add `CountTokensEndpoint` to `upstream.Profile`. Set it only for Anthropic (`/v1/messages/count_tokens`). `Client.CountTokens` returns `ERROR_UNSUPPORTED_FEATURE` with HTTP `501` for profiles without the endpoint; otherwise it uses `CountTokensMs`, the same auth/header safeguards, and returns the upstream response. Do not add a fixed count or chars/4 fallback.

- [ ] **Step 6: Run credential/client tests and commit**

Run: `gofmt -w proxy/upstream && go test -race ./proxy/upstream -count=1`

Expected: PASS.

```bash
git add proxy/upstream
git commit -m "feat(proxy): add safe credential and upstream transport"
```

### Task 13: Generic HTTP handler, server cutover, and 21-route integration matrix

**Files:**

- Create: `proxy/handler.go`
- Create: `proxy/handler_test.go`
- Create: `proxy/observability.go`
- Create: `proxy/server_test.go`
- Modify: `proxy/server.go:24-86`
- Modify: `cmd/proxy.go:22-32`

**Interfaces:**

- Consumes: `route.Router`, `transform.Registry`, `upstream.Catalog`, `CredentialResolver`, and `upstream.Client`.
- Produces: `Handler.Handle(format)`, `Handler.HandleModels`, `Handler.HandleCountTokens`, error-returning `proxy.New`, and the production request pipeline.

- [ ] **Step 1: Write the failing 21-case routing table**

Build one `httptest.Server` that records method/path/headers/body and returns a minimal successful response in the target profile's format. Define the table as:

```go
var providerCases = []struct {
	name           string
	credential     *auth.Credential
	qualifiedModel string
	wantProfile    string
	wantPath       string
}{
	{"anthropic", apiKeyCred("anthropic"), "anthropic/claude-3-5-sonnet-latest", "anthropic", "/v1/messages"},
	{"minimax", apiKeyCred("minimax"), "minimax/minimax-m3", "minimax", "/v1/messages"},
	{"openai api", apiKeyCred("openai"), "openai/gpt-5", "openai-api", "/v1/responses"},
	{"openai codex oauth", oauthCred("openai"), "openai/gpt-5", "openai-codex-oauth", "/codex/responses"},
	{"xai", apiKeyCred("xai"), "xai/grok-4.5", "xai", "/v1/responses"},
	{"xai forced chat", apiKeyCred("xai"), "xai-chat/grok-4.5", "xai", "/v1/chat/completions"},
	{"openai forced chat", apiKeyCred("openai"), "openai-chat/gpt-5", "openai-api", "/v1/chat/completions"},
}

var sourceCases = []struct {
	name   string
	format protocol.Format
	path   string
}{
	{"anthropic", protocol.FORMAT_ANTHROPIC_MESSAGES, "/v1/messages"},
	{"chat", protocol.FORMAT_OPENAI_CHAT, "/v1/chat/completions"},
	{"responses", protocol.FORMAT_OPENAI_RESPONSES, "/v1/responses"},
}

func apiKeyCred(provider string) *auth.Credential {
	return &auth.Credential{Provider: provider, Kind: auth.KIND_API_KEY, APIKey: "test-api-key"}
}

func oauthCred(provider string) *auth.Credential {
	return &auth.Credential{
		Provider: provider, Kind: auth.KIND_OAUTH, AccessToken: "test-access-token",
		RefreshToken: "test-refresh-token", ExpiresAt: time.Now().Add(time.Hour),
	}
}
```

Cross-product produces 21 route subtests. Inside each subtest, run `stream=false` and `stream=true`, for 42 HTTP exchanges total. Each mode asserts selected upstream path/auth, transformed model/body format, and that the downstream response decodes in the original source format. For Codex OAuth, assert both modes send `stream:true` upstream; the non-stream client receives source-format JSON only after the collector finishes, while the stream client receives source-format SSE.

- [ ] **Step 2: Implement the generic Handler and dependency constructor**

```go
type Handler struct {
	router      *route.Router
	registry    *transform.Registry
	catalog     *upstream.Catalog
	credentials *upstream.CredentialResolver
	client      *upstream.Client
	observer    TransformObserver
	maxBodyBytes int64
}

type HandlerDeps struct {
	Router      *route.Router
	Registry    *transform.Registry
	Catalog     *upstream.Catalog
	Credentials *upstream.CredentialResolver
	Client      *upstream.Client
	Observer    TransformObserver
	MaxBodyBytes int64
}

func NewHandler(deps HandlerDeps) (*Handler, error)
func (h *Handler) Handle(format protocol.Format) gin.HandlerFunc
func (h *Handler) HandleModels() gin.HandlerFunc
func (h *Handler) HandleCountTokens() gin.HandlerFunc
```

`NewHandler` rejects nil dependencies and non-positive `MaxBodyBytes`. Production passes `int64(cfg.BodyLimit) << 20`. Use `http.MaxBytesReader` for client request bodies, `io.LimitReader(resp.Body, maxBodyBytes+1)` for successful non-stream upstream bodies, and a fixed `64<<10` limit for upstream error bodies; detect the extra byte and return `ERROR_PROTOCOL` instead of processing truncated JSON.

Define redacted observability in `proxy/observability.go`:

```go
type TransformObserver interface {
	RecordWarning(context.Context, string, protocol.Format, protocol.Format, protocol.Warning)
	RecordLoss(context.Context, string, protocol.Format, protocol.Format, protocol.SemanticLoss)
}

func NewTransformObserver(logger *slog.Logger, meter metric.Meter) (TransformObserver, error)
```

`NewTransformObserver` rejects nil logger/meter and creates counters named `agentsdk.proxy.transform.warnings` and `agentsdk.proxy.transform.losses`. Both methods log only provider ID, source/target format, warning code or loss field, and increment the matching counter with those bounded attributes. They never log warning messages, loss reasons, body content, headers, or credentials. Add a fake-observer unit test proving each `TransformResult.Warning` and `TransformResult.Loss` is recorded once, and an OpenTelemetry manual-reader test proving both counters increment.

The handler emits one completion log with only `request_id`, routed model, provider profile ID, source/target format, stream mode, HTTP status, and duration. Use a valid incoming `x-request-id` or generate `uuid.NewString`; pass that value upstream through the allowlist and return it in the safe response headers. Never log the raw qualified model body, request/response payloads, tool data, or error causes at info level.

`Handle` reads the bounded raw body, parses model/stream metadata, resolves route and credential, selects concrete profile/target format, looks up the pair, transforms request, records every warning/loss through `TransformObserver`, applies profile normalization, updates `TranslatedRequest.Stream` to `NormalizedRequest.UpstreamStream`, calls upstream, and creates an `Exchange` containing original/translated envelopes, concrete profile ID, and `NewID: uuid.NewString`.

For non-2xx upstream responses, read a bounded safe error body, call `transform.DecodeUpstreamError`, encode for the source format, preserve status/`Retry-After`/safe request ID, and stop. For non-stream 2xx, read the bounded body, call the pair's reverse response transform, record its warnings/losses through the same observer, copy only safe response headers, and write `TransformResult.Body` with the source content type.

When `BridgeToNonStream` is true, require upstream `Content-Type: text/event-stream`, create the pair stream transformer plus `transform.NewStreamCollector(sourceFormat, exchange)`, and keep downstream headers uncommitted. Feed every upstream frame through the pair transformer, then every translated source frame into the collector. After upstream EOF, call both `Close` methods, feed any transform-close frames to the collector, and write the collector's JSON body as a normal `200` response. Any failure before collection completes returns a normal source-native HTTP error; partial SSE is never sent to a non-stream client.

For client streams, require upstream `Content-Type: text/event-stream`, parse full frames with `protocol.SSEDecoder`, push them through the pair's per-request transformer, write/flush every output frame, call `Close` on EOF, write/flush any close frames, and emit exactly one source-format terminal error on any post-header error:

| Source format | Terminal error frames |
|---|---|
| Anthropic Messages | `event: error` with `{"type":"error","error":{"type":"api_error","message":"stream terminated"}}` |
| OpenAI Chat | one `data: {"error":{"type":"api_error","code":"stream_error","message":"stream terminated"}}`, then `data: [DONE]` |
| OpenAI Responses | `event: response.failed` with `{"type":"response.failed","response":{"status":"failed","error":{"code":"stream_error","message":"stream terminated"}}}` |

Never include the internal/upstream error text in these frames. Return immediately on writer error or client cancellation so the upstream context is canceled.

- [ ] **Step 3: Add body-limit, unknown model, cancellation, truncation, and redacted-log tests**

Tests must assert:

- malformed JSON returns source-native `400` without contacting upstream;
- unknown model returns `400 unknown_model`, not Anthropic fallback;
- unsupported provider capability returns source-native `400` before upstream; a defensive registry lookup miss maps to `422`, although `NewDefaultRegistry` makes that state unreachable after successful startup;
- upstream `429` retains status and `Retry-After` in source error shape;
- canceled downstream context reaches the upstream server context;
- EOF before terminal event produces a source stream error;
- captured `slog` output contains model/provider/request ID but not prompt text, tool output, API keys, or bearer tokens.

- [ ] **Step 4: Implement models and token-count handlers**

`HandleModels` returns models from `Catalog.AdvertisedModels()` rather than the old inline list. `HandleCountTokens` parses the Anthropic count request, resolves the provider/profile, calls `Client.CountTokens`, and passes through a valid Anthropic native response. For any profile without native counting, return Anthropic-shaped `501 unsupported_feature`.

- [ ] **Step 5: Cut server construction over to the new handler**

Change:

```go
func New(cfg *config.ProxyConfig) (*Server, error)
```

Build `auth.FileStore`, `CredentialResolver`, default catalog/router, `transform.NewDefaultRegistry`, upstream client, `NewTransformObserver(slog.Default(), otel.Meter("github.com/bizshuk/agentsdk/proxy"))`, and Handler once. Any invalid dependency/registry/metric constructor returns a wrapped error; do not install `notImplemented` fallbacks for public model routes. Wire:

```go
v1.GET("/models", handler.HandleModels())
v1.POST("/chat/completions", handler.Handle(protocol.FORMAT_OPENAI_CHAT))
v1.POST("/responses", handler.Handle(protocol.FORMAT_OPENAI_RESPONSES))
v1.POST("/messages", handler.Handle(protocol.FORMAT_ANTHROPIC_MESSAGES))
v1.POST("/messages/count_tokens", handler.HandleCountTokens())
```

Update `cmd/runProxy` to handle `server, err := proxy.New(cfg)` before `server.Run(ctx)`.

- [ ] **Step 6: Run the integration matrix and proxy suite**

Run: `gofmt -w proxy cmd/proxy.go && go mod tidy && go test -race ./proxy/... -count=1`

Expected: PASS, including all 21 route cases, cancellation, truncation, errors, models, and token counting.

- [ ] **Step 7: Commit handler/server cutover**

```bash
git add proxy/handler.go proxy/handler_test.go proxy/observability.go proxy/server.go proxy/server_test.go cmd/proxy.go go.mod go.sum
git commit -m "feat(proxy): route all agent protocols through pairwise transforms"
```

### Task 14: Remove legacy adaptor, synchronize docs, and perform final verification

**Files:**

- Delete: `proxy/adaptor/adaptor.go`
- Delete: `proxy/adaptor/adaptor_test.go`
- Delete: `proxy/adaptor/translator.go`
- Delete: `proxy/adaptor/translator_test.go`
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `README.todo` only after preserving any current user edits

**Interfaces:**

- Consumes: completed new proxy implementation.
- Produces: one source of truth for transforms, current documentation, and verification evidence.

- [ ] **Step 1: Prove the legacy adaptor has no callers before deletion**

Run: `rg -n 'proxy/adaptor|adaptor\.' --glob '*.go' --glob '!tmp/**' .`

Expected: no production callers. Any remaining matches must be migrated to `proxy/handler`, `proxy/transform`, or the protocol DTO packages before proceeding.

- [ ] **Step 2: Delete the legacy package and verify no translation symbol remains duplicated**

Delete the four listed files, then run:

`rg -n 'TranslateAnthropicToOpenAI|TranslateOpenAIToAnthropic|TranslateAnthropicToResponses|TranslateResponsesToAnthropicMessage|proxyOpenAIResponsesTranslated' --glob '*.go' --glob '!tmp/**' .`

Expected: no matches under production/test code; replacements use the new directed transform names.

- [ ] **Step 3: Update README and CLAUDE structure/decisions**

In `README.md`, add a `Proxy protocol bridge` section with the 3×3 matrix, five concrete provider profiles, xAI Responses preference, and the route → pair → upstream → reverse-pair flow. In `CLAUDE.md`, replace the old `proxy/adaptor` tree with `protocol`, `transform`, `route`, `upstream`, and `handler.go`; add decisions for explicit registry coverage, provider/format separation, full SSE frames, unexpected EOF, and no unknown-model fallback.

For `README.todo`, first run `git status --short README.todo`. If it is clean, add one completed Archive item exactly describing `3×3 pairwise proxy transforms + provider profiles + SSE lifecycle`; if it is dirty, leave it untouched and report that the user's existing change was preserved.

- [ ] **Step 4: Run focused formatting, static checks, and race tests**

Run:

```bash
gofmt -w proxy cmd/proxy.go
go vet ./proxy/... ./cmd/...
go test -race ./proxy/... -count=1
go test ./cmd ./config ./auth/... -count=1
```

Expected: all commands PASS.

- [ ] **Step 5: Run root and workspace-module regression tests**

Run:

```bash
go test ./... -count=1 -timeout=60s
(cd provider/anthropic && go test ./... -count=1)
(cd provider/google && go test ./... -count=1)
(cd provider/openaicompat && go test ./... -count=1)
(cd mcp && go test ./... -count=1)
(cd sample/logdoctor && go test ./... -count=1)
(cd sample/file-agent && go test ./... -count=1)
```

Expected: all workspace submodule commands PASS. At plan-writing baseline, the root command has one unrelated existing failure, `app.TestRunRejectsEmptyName`; implementation must not add any new failure. If that baseline test has been independently fixed by execution time, require the root command to PASS completely.

- [ ] **Step 6: Verify acceptance criteria mechanically**

Run:

```bash
rg -n 'FORMAT_ANTHROPIC_MESSAGES|FORMAT_OPENAI_CHAT|FORMAT_OPENAI_RESPONSES' proxy/protocol proxy/transform
rg -n 'NewDefaultRegistry|NewRegistry' proxy/transform proxy/server.go
rg -n 'http\.DefaultClient|context\.Background\(\)|"body"' proxy
rg -n 'input_tokens.*100|unknown-model.*anthropic|return "anthropic"' proxy
git diff --check
```

Expected: the first two searches show the intended explicit contracts/wiring. The risky-pattern searches return no matches in request handling code; the server shutdown's existing `context.Background()` is allowed and must be the only proxy match. `git diff --check` returns no output.

- [ ] **Step 7: Commit cleanup and documentation**

Stage only the legacy deletions and clean documentation files. Do not stage a dirty pre-existing `README.todo`.

```bash
git add proxy/adaptor README.md CLAUDE.md
git add README.todo  # only when Step 3 established it was clean before this task
git commit -m "refactor(proxy): remove legacy one-to-one adaptor"
```

## Requirement Coverage

| Requirement | Planned evidence |
|---|---|
| Pairwise architecture remains | Tasks 3–10 explicit 9-pair registry; no canonical IR |
| All request combinations | Tasks 4–6 fixtures plus Task 10 exact 9/9 registry test |
| All non-stream response combinations | Task 7 six cross-format paths + Task 3 identities |
| All stream combinations | Tasks 8–10 lifecycle/equivalence/isolation tests |
| Stream-only upstream with non-stream client | Task 10 collectors + Tasks 11/13 Codex bridge tests |
| Provider and protocol stay separate | Task 11 router/catalog and Task 12 transport; transforms have no credential/HTTP imports |
| xAI protocol | Task 11 `https://api.x.ai`, Responses default `/v1/responses`, Bearer/JSON, forced Chat route |
| Credential refresh and persistence | Task 12 context propagation, active selection, refresh/save failure tests |
| Error/status/header safety | Tasks 1, 7, 11–13 typed errors, bounded bodies, allowlists, redaction |
| SSE correctness | Tasks 1, 8–10, 13 full frames, terminal lifecycle, unexpected EOF, cancellation |
| Warning/loss observability | Tasks 4–7 typed diagnostics and Task 13 redacted logs/OpenTelemetry counters |
| Native token count only | Tasks 12–13 Anthropic endpoint and source-native `501` elsewhere |
| Legacy removal and docs | Task 14 caller proof, deletion, README/CLAUDE synchronization |

## Execution Checkpoints

- After Task 3: protocol contracts and strict registry exist; production still uses legacy adaptor.
- After Task 6: all 9 request directions pass fixtures; production still uses legacy adaptor.
- After Task 10: all 27 protocol paths pass, including stream lifecycle/equivalence/isolation.
- After Task 12: provider routing, credential refresh, HTTP/auth/header/timeout behavior pass without handler cutover.
- After Task 13: production routes use the new pipeline and all 21 provider cases pass.
- After Task 14: legacy adaptor is removed, docs match code, and final verification evidence is recorded.
