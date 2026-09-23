# xAI Image Generation Implementation Plan

> `For agentic workers`: execute this plan with `test-driven-development`,
> `golang-code-quality`, and `verification-before-completion`.

`Goal`: expose `POST /v1/images/generations`, forward its OpenAI-compatible
payload to xAI Imagine with either an xAI API key or the existing refreshed
xAI OAuth bearer, and leave the Claude Code MCP/Skill integration as explicit
client-side work.

`Architecture`: keep image generation outside the three-format transform
matrix because its request and response are already OpenAI-compatible. Resolve
the existing `xai` credential, select the concrete profile by credential kind,
then use a dedicated image target on `api.x.ai`; OAuth image traffic must not
use the inference-only `cli-chat-proxy` headers. The handler passes the upstream
JSON response through unchanged so a later MCP tool can request `b64_json`,
save it locally, and return a path.

`Tech Stack`: Go 1.26, Gin, `net/http`, existing `auth` credential resolver,
Testify.

## Global Constraints

- Preserve the unrelated dirty `tmp/CLIProxyAPI` submodule state.
- Do not add image generation to `model.Format` or the pairwise transform matrix.
- Follow `grok-build` commit `b41c75a578f98bddbd326ab02cd53618451d97ee`:
  image generation uses `api.x.ai` directly for API-key and OAuth users.
- Keep the downstream response compatible with xAI/OpenAI
  `/v1/images/generations`.
- Do not implement the Claude Code MCP server or Skill in this task.

---

### Task 1: Add the xAI image upstream capability

`Files`:

- Modify: `svc/upstream/profile.go`
- Modify: `svc/upstream/profile_test.go`
- Modify: `svc/upstream/client.go`
- Modify: `svc/upstream/client_test.go`

`Interfaces`:

- Produces: `Profile.ImageGenerationBaseURL string`
- Produces: `Profile.ImageGenerationEndpoint string`
- Produces:
  `Client.GenerateImage(context.Context, Profile, *authmodel.Credential, model.RequestEnvelope) (*http.Response, error)`

- [ ] Add failing catalog tests requiring both `xai` profiles to expose
  `https://api.x.ai` plus `/v1/images/generations`.
- [ ] Add failing client tests proving API-key and OAuth credentials send the
  original request body to `/v1/images/generations` with the correct bearer.
- [ ] Assert the OAuth image request omits inference-only
  `X-XAI-Token-Auth`, `x-authenticateresponse`, and
  `x-grok-model-override` headers.
- [ ] Run:

  ```bash
  go test ./svc/upstream -run 'Test(DefaultCatalogCapabilities|ClientGenerateImage)' -count=1
  ```

  Expected before implementation: fail because the profile fields and
  `GenerateImage` method do not exist.

- [ ] Add the profile fields and defaults:

  ```go
  ImageGenerationBaseURL  string
  ImageGenerationEndpoint string
  ```

- [ ] Add an image-specific HTTP client using the Grok Build timeout contract:
  `300s` total and `240s` response-header wait.
- [ ] Implement `GenerateImage` so OAuth ignores the inference credential base
  URL, uses the OAuth access token as a bearer, and calls the public xAI image
  API without cli-chat-proxy authentication headers.
- [ ] Re-run the focused tests and require them to pass.

### Task 2: Expose the proxy image endpoint

`Files`:

- Create: `handlers/image_generation.go`
- Create: `handlers/image_generation_test.go`
- Modify: `handlers/server.go`
- Modify: `handlers/server_test.go`

`Interfaces`:

- Consumes: `Client.GenerateImage`
- Produces: `Handler.HandleImageGenerations() gin.HandlerFunc`
- Produces: `POST /v1/images/generations`

- [ ] Add a failing handler test using an OAuth credential and a local upstream.
  It must verify the exact request body and raw `b64_json` response round-trip.
- [ ] Add failing validation tests for malformed JSON, blank `model`, and blank
  `prompt`; each must stop before upstream I/O.
- [ ] Add a failing route-table test for
  `POST /v1/images/generations`.
- [ ] Run:

  ```bash
  go test ./handlers -run 'Test(HandleImageGenerations|NewServerWiresImageGenerationRoute)' -count=1
  ```

  Expected before implementation: fail because the handler and route do not
  exist.

- [ ] Implement request validation with this required wire subset while
  preserving all optional/unknown fields in the raw body:

  ```go
  struct {
      Model  string `json:"model"`
      Prompt string `json:"prompt"`
  }
  ```

- [ ] Resolve family `xai`, call `GenerateImage`, bound the response body,
  preserve safe response headers/status/body, and emit structured routed and
  completed logs.
- [ ] Register the route under the existing authenticated and rate-limited
  `/v1` group.
- [ ] Re-run the focused tests and require them to pass.

### Task 3: Synchronize documentation and leave client work explicit

`Files`:

- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `README.todo`

`Interfaces`:

- Documents: proxy image endpoint, direct xAI OAuth image transport, raw JSON
  pass-through boundary.
- Defers: Claude Code `image_gen` MCP tool and `/imagine` Skill.

- [ ] Add `/v1/images/generations` to the public endpoint table and document
  that both xAI credential kinds call `api.x.ai` for Imagine.
- [ ] Add `handlers/image_generation.go` and the dedicated upstream capability
  to the technical context.
- [ ] Add open TODO items for the Claude Code MCP tool, `/imagine` Skill, and
  Base64-to-file/path result handling.
- [ ] Format and run:

  ```bash
  gofmt -w handlers/image_generation.go handlers/image_generation_test.go handlers/server.go handlers/server_test.go svc/upstream/profile.go svc/upstream/profile_test.go svc/upstream/client.go svc/upstream/client_test.go
  go test ./... -count=1
  go vet ./...
  go build ./...
  git diff --check
  ```

- [ ] Inspect `git status --short` and confirm `tmp/CLIProxyAPI` remains
  untouched.
