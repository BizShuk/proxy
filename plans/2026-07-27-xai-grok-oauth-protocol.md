# xAI Grok OAuth Protocol Implementation Plan

> `grok-build` reference: xai-org/grok-build commit
> `b41c75a578f98bddbd326ab02cd53618451d97ee` (2026-07-26).

`Goal`: make persisted `xai` OAuth credentials use the Grok CLI inference
surface and support all three wire protocols already represented by the proxy:
OpenAI Responses, OpenAI Chat Completions, and Anthropic Messages.

`Architecture`: keep public xAI API-key traffic on `api.x.ai`, add a separate
`xai-grok-oauth` concrete profile for OAuth traffic, and select the concrete
profile from the credential kind. Reuse the existing complete pairwise
transform matrix. Put request-shape compatibility in the profile normalizer,
transport headers in the upstream client, and format selection in the router.

`Tech stack`: Go 1.26, `net/http`, Gin, existing `auth` credential resolver,
existing `svc/transform` registry, Testify.

## Task 1: Lock the OAuth profile and routing contract

`Files`:

- Modify: `svc/upstream/profile_test.go`
- Modify: `svc/route/router_test.go`
- Modify: `svc/upstream/profile.go`
- Modify: `svc/route/router.go`

`Steps`:

1. Add failing catalog tests proving API keys still select `xai`, while OAuth
   selects `xai-grok-oauth` at `https://cli-chat-proxy.grok.com/v1`.
2. Assert that OAuth supports `/responses`, `/chat/completions`, and
   `/messages`, with Responses as the default.
3. Add failing router tests for `xai-responses/`, `xai-chat/`, and
   `xai-messages/`.
4. Run:

   ```bash
   go test ./svc/upstream ./svc/route -run 'Test(DefaultCatalogCapabilities|CatalogResolveProfile|RouterResolve)' -count=1
   ```

   Expected: fail because the OAuth profile and format qualifiers do not exist.
5. Implement credential-kind profile selection and generic qualifier-to-format
   resolution.
6. Re-run the focused tests and require them to pass.

## Task 2: Implement Grok request normalization and SSE compatibility

`Files`:

- Modify: `svc/upstream/profile_test.go`
- Modify: `svc/upstream/profile.go`
- Modify: `model/sse_test.go`
- Modify: `model/sse.go`

`Steps`:

1. Add failing tests for OAuth Responses normalization:
   - preserve raw xAI tools such as `{"type":"x_search"}`;
   - default `store` to `false` without overwriting an explicit value;
   - append `reasoning.encrypted_content` without duplicates;
   - preserve unknown request fields.
2. Add failing tests for streaming Chat:
   - force `stream:true`;
   - merge `stream_options.include_usage:true`;
   - preserve existing `stream_options` fields.
3. Add failing tests for Messages:
   - support the native Anthropic shape;
   - default missing/zero `max_tokens` to `128000`;
   - force `stream:true` for streaming requests.
4. Add a failing SSE test with a UTF-8 BOM before the first event field.
5. Run:

   ```bash
   go test ./svc/upstream ./model -run 'TestXAIGrokOAuth|TestSSEDecoderStripsUTF8BOM' -count=1
   ```

   Expected: fail before the compatibility code exists.
6. Implement the normalizer with raw JSON mutation after protocol validation,
   and strip a BOM only at the beginning of an SSE stream.
7. Re-run the focused tests and require them to pass.

## Task 3: Implement the OAuth transport header contract

`Files`:

- Modify: `svc/upstream/client_test.go`
- Modify: `svc/upstream/client.go`
- Modify: `svc/upstream/profile.go`

`Steps`:

1. Add a failing client test that captures the outgoing OAuth request and
   asserts:
   - `Authorization: Bearer <access token>`;
   - `X-XAI-Token-Auth: xai-grok-cli`;
   - `x-authenticateresponse: authenticate-response`;
   - `x-grok-client-mode: headless`;
   - compatible client version, identifier, and User-Agent;
   - request, conversation, session, agent, model, turn, deployment, and user
     metadata behavior.
2. Assert that downstream callers cannot override fixed auth/client identity
   headers, and that the credential account ID wins for `x-grok-user-id`.
3. Assert that Grok response metadata headers are allowlisted:
   `x-grok-context-window`, `x-grok-max-completion-tokens`,
   `x-models-etag`, and `x-should-retry`.
4. Run:

   ```bash
   go test ./svc/upstream -run 'TestClientDoAppliesXAIGrokOAuthProtocol' -count=1
   ```

   Expected: fail because those headers are not applied.
5. Implement fixed and request-scoped header injection in the upstream client.
6. Re-run the focused test and require it to pass.

## Task 4: Repair xAI credential-to-Grok dispatcher mapping

`Files`:

- Modify: `svc/upstream/dispatcher_oauth_test.go`
- Modify: `svc/upstream/dispatcher_oauth.go`

`Steps`:

1. Change tests to use the persisted auth provider ID `xai` for API-key and
   OAuth credentials while expecting the Agents SDK provider ID `grok`.
2. Add an integration test proving `NewDispatcherWithAuth` asks the resolver
   for `xai` and registers Grok.
3. Run:

   ```bash
   go test ./svc/upstream -run 'Test(BuildProvider.*XAI|NewDispatcherWithAuthResolvesAllFamilies)' -count=1
   ```

   Expected: fail because the dispatcher currently asks for `grok`.
4. Map auth family `xai` to the Agents SDK Grok constructors and change the
   resolver family list to `xai`.
5. Re-run the focused tests and require them to pass.

## Task 5: Verify the complete HTTP request lifecycle

`Files`:

- Modify: `handlers/handler_test.go`

`Steps`:

1. Add OAuth provider cases for default Responses, forced Chat, and forced
   Messages.
2. Verify every supported downstream source format reaches the selected OAuth
   endpoint through the existing pairwise transform matrix.
3. Verify stream and non-stream paths, normalized request body, and OAuth
   headers at the fake upstream.
4. Run:

   ```bash
   go test ./handlers -run 'TestHandlerRoutesAllProviderAndSourceCombinations' -count=1
   ```

   Expected: fail before the new OAuth cases are supported, then pass after the
   profile/router/client work.

## Task 6: Synchronize project documentation and verify

`Files`:

- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `README.todo`

`Steps`:

1. Document the API-key/OAuth split, three OAuth protocols, qualifiers,
   endpoints, fixed headers, request defaults, and credential ownership.
2. Record the completed implementation under `README.todo` archive without
   changing the unrelated open TODO.
3. Format and run fresh verification:

   ```bash
   gofmt -w model/sse.go model/sse_test.go svc/route/router.go svc/route/router_test.go svc/upstream/profile.go svc/upstream/profile_test.go svc/upstream/client.go svc/upstream/client_test.go svc/upstream/dispatcher_oauth.go svc/upstream/dispatcher_oauth_test.go handlers/handler_test.go
   go test ./... -count=1
   go vet ./...
   go build ./...
   git diff --check
   ```

4. Inspect `git status --short` and confirm `tmp/CLIProxyAPI` remains untouched.

## Task 7: Preserve typed reasoning across Anthropic tool loops

`Files`:

- Modify: `model/responses/types.go`
- Modify: `svc/transform/anthropic_responses_request.go`
- Modify: `svc/transform/anthropic_responses_response.go`
- Modify: `svc/transform/anthropic_responses_stream.go`
- Test: `svc/transform/anthropic_responses_request_test.go`
- Test: `svc/transform/anthropic_responses_stream_test.go`

`Steps`:

1. Reproduce the Claude Code tool loop where an assistant `thinking` block is
   returned before `tool_use`, then rejected on the next request.
2. Convert Anthropic `thinking` into an ordered Responses `reasoning` input
   item rather than rejecting it.
3. Preserve `id`, `summary`, and `encrypted_content` through a versioned,
   opaque Anthropic `thinking.signature`.
4. Emit the same metadata as `signature_delta` when a streaming Responses
   reasoning item completes.
5. Verify a live `claudex` Bash tool loop against an isolated proxy instance,
   then run the full Go test, vet, build, and diff checks.
