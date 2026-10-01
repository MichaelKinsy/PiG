# Lane port-992-core: Pi 0.99.2 non-MCP changes

Upstream: Pi v0.99.2 (`005af57d88ee23b33778f343a9595b32e67ff788`), diffed against v0.99.1 for `ai`, `agent`, `coding-agent`, `tui`. Citations below use `.upstream/v0.99.2/...` (the lead's mirror regeneration will create that path; until then the tree is a temporary clone).

## Scope map

| Change | Upstream | PiG |
|---|---|---|
| Anthropic workload identity federation (#10177, #10242) | `ai/src/api/anthropic-messages.ts`, `providers/anthropic.ts`, `env-api-keys.ts`; tests `anthropic-federation.test.ts`, `anthropic-federation-sdk.test.ts` | `ai/` |
| Anthropic strict tool schema keywords (#9953) | `anthropic-messages.ts`, `constrained-sampling.ts`; test `anthropic-strict-tool-schema.test.ts` (moved out of `anthropic-eager-tool-input-compat.test.ts`) | `ai/` |
| Z.AI CN overflow (#10208) | `utils/overflow.ts`; `overflow.test.ts` | `ai/` |
| Retry-After unparseable (#9571) | `utils/provider-retry.ts` (no upstream test changed; PiG tests are new) | `ai/` |
| `/reload` enables tools newly added to `defaultTools` (#10245) | `agent-session.ts`, `sdk.ts`; `default-tools-setting.test.ts` | `coding/` |
| Provisional configured native providers (#9962, #10190) | `model-runtime.ts`; `model-runtime-modify-models-compat.test.ts` | `coding/` |
| Branch selection one catalog lookup (#10198) | `virtual-models.ts`; `virtual-models.test.ts` | `coding/` |
| Linear remote catalog merge | `remote-catalog-provider.ts` (no upstream test) | `coding/` |
| Extension command validation (#10054) | `extensions/loader.ts` (no upstream test) | every host and SDK |

## Red runs

### ai (red commit)

Command: `go test ./ai -run 'TestAnthropicStrict|TestStrictSamplingProvider|TestAnthropicUpstreamEager|TestOverflowUpstream|TestProviderRetryDelay|TestRetryTransport_Unparseable|TestAnthropicWorkload|TestAnthropicFederation|TestAnthropicTokenCache'`.

Stubs: `resolveJSONSchemaStrictSampling` takes the provider keyword check (every other provider passes nil), `anthropicStrictUnsupportedKeyword` returns false, `anthropicTokenCache` returns zero values, the five `Anthropic*Env` constants exist. The federation, strict-keyword, overflow, retry-delay and token-cache tests fail because the behavior is missing:

- `TestAnthropicWorkloadIdentityFederation/*`: the resolver returns nil, no exchange is sent, and a keyless `kimi-coding` stream sends a request instead of failing with `No API key for provider: kimi-coding` (Pi asserts request auth in `stream`, `anthropic-messages.ts:614`).
- `TestAnthropicFederation*` (SDK behaviors): no `/v1/oauth/token` request is ever made.
- `TestAnthropicStrictToolSchemas/sends prefer tools non-strict...`: `minimum`/`maximum`, `minItems: 2` and `format: regex` tools are sent strict.
- `TestOverflowUpstream/detects z.ai CN endpoint...`: `Prompt exceeds max length` is not an overflow.
- `TestProviderRetryDelayUnparseableRetryAfter`, `TestRetryTransport_UnparseableRetryAfterWaitsForBackoff`: an unparseable `Retry-After` yields a zero delay and the request is retried at once; numeric prefixes such as `5 seconds` are not read.
- `TestAnthropicTokenCache/*`: the stub never calls the provider.

Two ported cases pass before the fix because PiG never ran the SDK credential chain: `TestAnthropicFederationSkippedForHeaderOwnedAuth` and the `ANTHROPIC_SERVICE_ACCOUNT_ID`/precedence parts of `TestAnthropicWorkloadIdentityFederation`. They guard regressions in the green change. `anthropic-eager-tool-input-compat.test.ts` lost its strict-schema case upstream; the Go twin moved with it.

Not ported: `models-entry.test.ts` (the lightweight `@earendil-works/pi-ai/models` JavaScript entry point and its import-graph check; Go has no package entry graph to slim, so it is designed out).

### coding, extension hosts and SDKs (red commit)

Environment: every command runs with a temporary `HOME`, `PIG_HOME` and `PIG_CODING_AGENT_DIR` (`/tmp/p992/env.sh`). Python tests run with pytest from a throwaway `uv` venv (`/tmp/uvpy`); pytest is not installed on the host.

Stubs: `Session.ReloadSettings` is an empty method. No other production stub was needed (the host and SDK validations are behavior only).

Red runs:

- `go test ./coding -run TestDefaultToolsReloadPort`: `active after adding = [edit read write], want [edit grep inactive_tool read write]`; the excluded-tools and CLI-defaults cases fail the same way. The explicit-option cases (`--tools`, `--no-tools`/builtin) pass before the fix because nothing is activated yet; they pin the option precedence that the green change must keep (mutation-checked there).
- `go test ./coding -run TestModelRuntimeNativeCompatibilityUpstream`: `configured=false oauth=false right after registration, want both`; the API-key case reports no provisional auth.
- `go test ./coding -run TestGetBranchSelectionLooksUpOnlyTheLastModelChange`: 100 lookups for a 100-message branch instead of `[faux/small]`. Benchmark before the fix (`BenchmarkGetBranchSelection`, 2000 assistant messages, one `model_change`): 26.3 ms/op, 2000 lookups/op, 5.2 MB/op, 106009 allocs/op.
- `go test ./coding/extension/host/subprocess -run 'TestNodeRunnerRejectsInvalidCommand|TestCommandNamesValidated'`: the Node runtime loads commands without a handler, the Host reports `command name is required` for an undefined name and fails decoding a numeric name, and `validateRegisterPayload` returns `command name is required` instead of Pi's message.
- `extensions/sdk`: `TestCommandValidationBeforeRegistration` (Go SDK), `extensions/sdk-py/tests/test_command_validation.py` (Python), `command_name_validation_precedes_registration` (Rust): none of the SDKs rejects an empty name or a missing handler.

Findings recorded for the lead:

- Upstream `remote-catalog-provider.ts` (quadratic `mergeModels`) has no PiG counterpart: PORT_MAP records remote catalog refresh as not implemented (owner-approved), so there is nothing to fix or test.
- `built-in-tool-renderer.ts` and `minimal-mode.ts` ship only inside the vendored `pi-dist` tree (`coding/extension/host/subprocess/runtime-node/shims/pi-dist/pi-coding-agent/examples/extensions/`), which is regenerated by `automation/gen/vendor-pi-dist.sh` from the npm package (byte-for-byte rule). The example fix and `tool-renderer-examples.test.ts` arrive with the 0.99.2 re-vendor, not by editing the vendored files.

## Oracle golden regeneration (lead extra, Pi 0.99.2)

Source: `extensions/sdk-ts` `npm ci --ignore-scripts` (real `@earendil-works/pi-coding-agent@0.99.2`), each recipe in `coding/testdata/rpc33-observation/**/README.md`, `ai/testdata/google-tick-order/README.md`, the `credential-expiry.mjs` and `bash-structured-oracle.mjs` headers, and `check.mjs --oracle` for `pi-start.json`. Probes run with private HOME/agent dirs.

Regenerated (22 files stamped 0.99.1): rpc33 `inputs.json` (top, anthropic, azure, bedrock, google), `anthropic-messages/pi.json`, azure `rpc.json` and `ticks.json`, bedrock `pi.json` and `chain.json`, `faux/pi.json`, `test-faux/pi.json`, google `pi.json`, mistral `pi/ticks/realticks.json`, openai-codex `pi.json`, pi-messages `pi.json` and `ticks.json`, `pi-start.json`, `ai/testdata/credential-expiry.json`, `ai/testdata/google-tick-order/pi.json` (three runs byte-identical), `internal/codingagent/tools/testdata/bash-structured-oracle.json`. Not stamped and therefore untouched: rpc33 top `pi.json`, azure `pi.json`.

Structural diff against the previous goldens (`/tmp/p992/jdiff.py`, stamp replaced before comparing):
- anthropic-messages `pi.json`: 0 differences besides `piVersion`. Byte size is identical (4171374) before and after; the 4164674 vs 4169626 figures in the lead note do not reproduce here. Federation and strict-schema changes are not exercised by the probe's fixtures (no key-less anthropic request, no tool with rejected keywords), so the recorded wire did not change.
- inputs, azure rpc/ticks, bedrock chain, mistral, codex, pi-messages, credential-expiry, google tick order, bash oracle: 0 differences besides the stamp (clocks in these are virtual).
- pi.json files of azure/bedrock/google/test-faux: only wall-clock `timestamp` and generated ids differ (real Date in the probe), as the README says for "first fresh execution".
- `faux/pi.json`: besides timestamps, `usage.input/cacheWrite/totalTokens` of the RPC records fall by 8 tokens (459 -> 451). Cause: the model-visible tool descriptions changed in Pi 0.99.2 (`extensions/tool-search/tool.ts` `TOOL_SEARCH_DESCRIPTION` no longer lists sources; `extensions/codemode/tool.ts` guidance text). PiG's faux observation tests still pass with the regenerated file, so PiG's prompt already produces the same estimate (or the faux path excludes these tools); not investigated further, no production change.

After regeneration: `TestAnthropicMicrotaskTrace`, `TestAnthropicPublishesMutationsWithoutAPush`, `TestAzureResponsesTickOrder`, `TestCredentialExpiryMatchesPi`, `TestFauxObservationOracle`, `TestTestFauxObservationOracle`, `go test ./coding -run 'RPC33|Observation'`, `./cmd/pig -run 'Faux|Observation'`, `./internal/codingagent/tools -run Bash` pass.

## Phase 1 status (port only; owner-approved method change)

Upstream test files changed v0.99.1..v0.99.2, non-durable, and their status in this lane:

| upstream file | status | Go test(s) | result |
|---|---|---|---|
| ai/test/anthropic-eager-tool-input-compat.test.ts | ported (strict-schema case moved out, as upstream) | `ai/anthropic_eager_upstream_test.go` | passing |
| ai/test/anthropic-strict-tool-schema.test.ts (A) | ported | `ai/anthropic_strict_tool_schema_upstream_test.go`, `anthropic_strict_keywords_test.go` | passing |
| ai/test/anthropic-federation.test.ts (A) | ported (wire-level: no SDK, the exchange and requests are observed on a loopback server) | `ai/anthropic_federation_upstream_test.go` | passing |
| ai/test/anthropic-federation-sdk.test.ts (A) | ported, plus SDK 0.124.0 behavior pins (token-cache.mjs, oidc-federation.mjs, types.mjs, client.mjs) | `ai/anthropic_federation_sdk_upstream_test.go`, `ai/anthropic_token_cache_test.go` | passing |
| ai/test/overflow.test.ts | ported (Z.AI CN case) | `ai/overflow_upstream_test.go` | passing |
| ai/test/models-entry.test.ts (A) | designed out: a JS `/models` subpath entry point with no Go equivalent | none | n/a |
| coding-agent/test/default-tools-setting.test.ts | ported | `coding/session_tool_registry_port_test.go` `TestDefaultToolsReloadPort` | FAILING: `active after adding = [edit read write], want [edit grep inactive_tool read write]` (also the excluded-tools and CLI-defaults cases); the option-precedence cases pass vacuously |
| coding-agent/test/model-runtime-modify-models-compat.test.ts | ported | `coding/model_runtime_native_upstream_test.go` `TestModelRuntimeNativeCompatibilityUpstream` | FAILING: `configured=false oauth=false right after registration, want both`; API-key case: `stored API-key provider auth = <nil>` |
| coding-agent/test/virtual-models.test.ts | ported | `coding/virtual_models_upstream_test.go` `TestGetBranchSelectionLooksUpOnlyTheLastModelChange`, `BenchmarkGetBranchSelection` | FAILING: one catalog lookup per assistant message (100 lookups, want 1); wrong model when the last `model_change` has no later response |
| coding-agent/test/tool-renderer-examples.test.ts (A) | NOT ported: it loads `examples/extensions/{built-in-tool-renderer,minimal-mode}.ts` into a session and renders the edit tool with `ToolExecutionComponent`. PiG carries those examples only as vendored byte-copies under `runtime-node/shims/pi-dist/` (0.99.1 until the re-vendor), so there is no 0.99.2 example to load and no Go edit-tool shell renderer oracle. Needs the re-vendor first | none | blocked |
| coding-agent/test/{codemode-renderer,codemode-worker-config,tool-search,mcp-command,mcp-extension}.test.ts, suite/agent-session-{codemode,mcp,mcp-oauth}.test.ts, suite/mcp-oauth-server.ts, codemode/test/sandbox.test.ts, mcp/test/streamable-http.test.ts | not in this lane: MCP and codemode belong to `port-992-mcp`. `tool-search.test.ts` and `codemode-renderer.test.ts` change with the MCP hidden-declaration work (the tool_search description no longer lists sources) | none | out of scope (confirm the owner) |

Tests with no upstream counterpart that pin additional 0.99.2 behavior (added in the red commits): Retry-After with an unparseable date (`ai/provider_retry_test.go`: `TestProviderRetryDelayUnparseableRetryAfter`, `TestRetryTransport_UnparseableRetryAfterWaitsForBackoff`; upstream provider-retry.ts has no test change), command registration validation (#10054: `coding/extension/host/subprocess/upstream_command_validation_test.go`, `extensions/sdk/command_validation_test.go`, `extensions/sdk-py/tests/test_command_validation.py`, Rust `command_name_validation_precedes_registration`).

FAILING in phase 1 (all mirror a not-yet-made production fix, none is a mis-port):
- `TestDefaultToolsReloadPort` (3 subtests), `TestModelRuntimeNativeCompatibilityUpstream` (2), `TestGetBranchSelectionLooksUpOnlyTheLastModelChange` (3).
- `TestNodeRunnerRejectsInvalidCommandRegistration` (6 subtests: Node runtime loads commands without a handler; Host reports `command name is required` / a decode error instead of Pi's message), `TestCommandNamesValidatedBeforeDeduplication`.
- Go SDK `TestCommandValidationBeforeRegistration` (3), Python `test_command_validation.py` (6), Rust `command_name_validation_precedes_registration`.
- Not mine, environment: `TestStreamUpstream` needs the shared `.upstream/v0.99.2` mirror.

Production changes already made before the method change (kept in their own commits):
- `b69fba184` fix(ai): Anthropic strict keyword fallback (#9953), Z.AI CN overflow (#10208), unparseable Retry-After backoff (#9571); `jsParseFloat` moved from the Copilot file.
- `ae1d527f0` feat(ai): Anthropic workload identity federation (#10177, #10242): `ai/anthropic_federation.go`, wiring in `anthropic.go`, `anthropic_client.go`, `auth_providers.go`, `direct_simple.go`. `-race -count=24 GOMAXPROCS=4` on the token cache and federation tests passes. Not yet done for these: mutation checks, user docs, and a keyless `kimi-coding` stream check through the real binary.
- Known limits of the federation port: the Node error text for file-system failures (`ENOENT: ...`) is Go's `open <path>: no such file or directory`; a fractional `expires_in` is truncated to whole seconds; the exchange is not retried per provider retry (same as upstream, which exchanges through the SDK's own fetch).
- Not started (phase 2): `Session.ReloadSettings`/default-tools reload (#10245), provisional native provider registration (#9962, #10190), `GetBranchSelection` (#10198), command validation (#10054) in Node runtime, Host and the three SDKs.

## Mutation checks of the kept ai fixes
Each mutation compiles and is killed (failing tests in brackets): federation 401 invalidation disabled [1]; advisory threshold 120 -> 10 [4]; forced refresh joins an in-flight one [2]; client cache ignores the fetch client [2]; Anthropic strict `minItems` accepts 0 and 1 only -> 0 only [3]; Retry-After `Infinity` no longer falls through to backoff [2]. Docs: `docs/site/docs/providers.md`, `changelog.d/port-992-anthropic-federation.md`.

## Phase 2 (green)

### #10198 GetBranchSelection (green)
`coding/virtual_models.go` scans the branch backward and looks up only the last `model_change` (virtual-models.ts:126-157). `go test ./coding -run 'VirtualModel|BranchSelection'` passes. `BenchmarkGetBranchSelection` (2000 assistant messages, one `model_change`): 26.3 ms/op, 2000 lookups/op, 5.2 MB/op, 106009 allocs/op before; 18.4 us/op, 1 lookup/op, 2.7 KB/op, 55 allocs/op after.

### #9962, #10190 native provider provisional auth (green)
`internal/codingagent/native_model_runtime.go` `RegisterNativeModelsProvider` now passes a provisional `AuthCheck` (oauth when the provider has OAuth and no API key, otherwise api_key; model-runtime.ts:885-917) to `syncRegistration`, whose snapshot update marks the provider configured when a credential is stored or request auth is configured. Mutation: passing nil again fails both subtests (`configured=false oauth=false`, `auth = <nil>`).
Test correction (not an upstream-input change): the red test stored the credential after `NewServices`, but Pi's test stores it in the `AuthStorage` passed to `ModelRuntime.create`, whose create-time refresh seeds `storedProviders` (model-runtime.ts:359). The test now writes `auth.json` before `NewServices`, which is the same ordering. `go test ./coding ./internal/codingagent`: only the item-1 `TestDefaultToolsReloadPort` subtests still fail.

### #10245 /reload activates tools newly added to defaultTools (green)
- `coding/session_tools.go` `Session.ReloadSettings` snapshots the resolved defaultTools, reloads the settings and records the newly added names when the initial tools came from the setting (`usesDefaultTools`, sdk.ts:448: no `--tools`, no `--no-tools`, no `--no-builtin-tools`; the CLI's resolved `ActiveBuiltinTools` counts as defaults). `RefreshTools` activates them (agent-session.ts:3591-3609). `SettingsManager.ResolvedDefaultTools` is `getDefaultTools() ?? DEFAULT_TOOL_NAMES`.
- Interactive wiring (`internal/codingagent/interactive_commands.go`, `reload_resources.go`): the `/reload` closure calls `ReloadSettings` through the Session; the branch without an extension runner replacement now also runs `RefreshTools` (`refreshToolsAfterReload`); `refreshAgentTools` restores the Session's active selection after it rebuilds the agent's tool list, which previously reset the selection to the startup set on every reload.
- New caller-path test: `internal/codingagent` `TestReloadActivatesToolsNewlyAddedToDefaultTools` (real Session + interactive reload owner: grep added, read kept after `-read`, bash stays off).
- Mutations (all compile): `ReloadSettings` not called by `/reload` -> fails; every name counted as added -> 3 fail; empty previous snapshot -> 3 fail; `usesDefaultTools` always true -> 2 fail; selection not restored in `refreshAgentTools` -> 1 fail.
- `go test ./coding ./internal/codingagent` pass.

### #10054 command registration validation (green)
Node runtime `registerCommand` (runtime.mjs) throws Pi's two messages with the extension entry, so the factory load fails; the Host `validateRegisterPayload` rejects an empty command name before `lastRegistrationWins` (covers every SDK's register payload); the Go SDK (`Command`, `RegisterCommand` -> `validateCommand`), Python SDK (`command` raises `ValueError` for a non-str/empty name or non-callable handler) and Rust SDK (`command` asserts the name; the handler is a required closure) validate at registration. The `extensiontest` Fake and the `extension.API` interface only record calls like their `RegisterTool`, so they are unchanged. The embedded `runtime-node.zip` and its digest were regenerated with `go generate ./coding/extension/host/subprocess`. Row added to `docs/extension-api-parity.md`.
Tests: `go test ./coding/extension/host/subprocess -run 'InvalidCommand|ValidCommand|CommandNamesValidated'`, `go test ./extensions/sdk/...`, `pytest extensions/sdk-py/tests` (all), `cargo test` in `extensions/sdk-rs` (52 lib tests).

### Gates (phase 2)
`go build ./...`, `go vet` (ai, coding, internal/codingagent), `GOOS=windows go vet` (ai, coding, subprocess, internal/codingagent), gofmt, `golangci-lint` on ai/coding/subprocess/internal/codingagent: 0 issues. `go fix -diff` is empty for this lane's files (two older hunks in `coding/virtual_models.go` line ~226 and `runtimecell/build_failure.go` predate it). `GOMAXPROCS=4 go test -race`: ai, coding pass; `-count=24` on the token cache, federation and interactive reload tests pass. The subprocess package passes with the real HOME (the temporary-HOME runs break the mise python shim: every python SDK subtest then fails with `mise ERROR`), except `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` (no clipboard in the sandbox, unrelated).
Two PiG-written tests had to follow Pi 0.99.2's command validation (loader.ts:302-311): `TestValidateRegisterPayloadRejectsInvalidRegistration/empty_command` now expects Pi's message, and the `TestNodeActionsThrowDuringFactory` probe commands define a handler (they carried their result in `description` without one, which Pi now rejects).
Item 5 (VisualLinePreview, bash and codemode renderers): already implemented by `port-992-mcp` (c8eac53bb, `tui.NewVisualLinePreview`, `tool_render_shell.go`, `tool_codemode_renderer.go`); not duplicated here.
