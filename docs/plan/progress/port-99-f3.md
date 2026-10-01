# Lane port-99-f3: ai classifiers and OAuth (upstream 0.99.1)

Lane `port-99-f3`, base `porter/pi-0.99.1` (40001cd42). Scope: plan Phase 2 step 3. Families `oauth` and `ai-sdk` (classifier and OAuth parts only).

The published package cooldown is not in effect on this host for the 0.99.1 pin: `npm install --save-exact --ignore-scripts @earendil-works/pi-ai@0.99.1` succeeded in a scratch directory without any override and without touching a global npm config. The classifier catalog data comes from that install.

## Red (tests with signature stubs)

Ported upstream test files, with Pi's inputs and expectations:

| upstream test | Go test | cases |
|---|---|---|
| `ai/test/typesafe-system-one.test.ts` | `ai/typesafe_system_one_upstream_test.go` | 9 |
| `ai/test/classifier-models.test.ts` | `ai/classifier_models_upstream_test.go` | 5 (`it.each` has 3 rows: 7 subtests) |
| `ai/test/cloudflare-workers-ai-system-one.test.ts` | `ai/cloudflare_workers_ai_system_one_upstream_test.go` | 4 |
| `ai/test/llama-cpp-classify.test.ts` | `ai/llama_cpp_classify_upstream_test.go` | 15 |
| `ai/test/oauth-callback-server.test.ts` | `ai/oauth_callback_server_upstream_test.go` | 12 |
| `ai/test/openai-chatgpt-oauth.test.ts` | `ai/openai_chatgpt_oauth_upstream_test.go` | 6 |
| `ai/test/openai-responses-chatgpt-sign-in.test.ts` | `ai/openai_responses_chatgpt_sign_in_upstream_test.go` | 4 subtests (`it.each` has 2 rows) |
| `ai/test/oauth-auth.test.ts` (changed) | `ai/oauth_auth_upstream_test.go` | +1 case, 1 changed list |
| `ai/test/anthropic-oauth.test.ts` (changed) | `ai/anthropic_oauth_upstream_test.go` | +1 |
| `ai/test/radius-oauth.test.ts` (changed) | `ai/oauth_radius_test.go` | +1 |
| `ai/test/openai-codex-oauth.test.ts` (changed) | `ai/oauth_openai_codex_upstream_test.go` | +1 |
| `ai/test/openrouter-oauth.test.ts` (changed) | already ported by family 1 (`ai/oauth_openrouter_test.go`, cites v0.99.1) | 0 |

Per-case dispositions:

- `classifier-models.test.ts:94` "rejects chat models at the classifier entry point at runtime" casts a chat model to `ClassifierModel<ClassifierApi>`. `Models.Classify` takes `*ClassifierModel`, so a chat model cannot reach it in Go. The type is the check. The reachable runtime rejection (provider without a classifier implementation, `does not support classification`, `models.ts:974`) has a test in its place.
- Classifier answers, questions and probabilities are ordered slices, not maps: JavaScript object insertion order is observable in prompts and results.
- `openai-responses-chatgpt-sign-in.test.ts` "keeps those fields" rows pass before and after: existing behavior, guard for the change.
- `openai-codex-oauth.test.ts:488` "falls back to the pasted redirect URL" passes before the refactor (the Go flow already falls back). It guards the shared callback server refactor.

Signature stubs and mechanical edits the upstream signature change forced:

- `OAuthAuth.Login` takes `LoginOptions` (`auth/types.ts:201-227`); `Models.Login` takes optional `LoginOptions`. Existing tests and `internal/codingagent/native_provider_oauth.go` were adapted mechanically (`ai/auth_native_login_test.go`, `ai/oauth_kimi_test.go`, `ai/oauth_openrouter_test.go`, `ai/auth_catalog_adapter_test.go`, `ai/auth_storage_lock_failure_upstream_test.go`, `ai/models_runtime_basic_upstream_test.go`, `coding/model_runtime_credential_sync_test.go`, `internal/codingagent/login_provider_projection_test.go`).
- New stubs: `ai/classifier.go` (types, `Models.Classify`), `ai/system_one.go`, `ai/llama_cpp_classify.go`, `ai/oauth_callback_server.go`, `ai/builtin_providers.go`.

Data-dependent cases that were waiting for the 0.99.1 package are ported here: classifier-models rows 3 to 5 and the four Cloudflare cases.

Not in this lane (other families own them): `model-runtime-classifiers.test.ts` and `llama-extension.test.ts` (coding-agent, family 6), `openai-responses-usage-limit.test.ts` and the retry test (family 2).

Red run (`go test ./ai -run '<the new and changed tests>' -count=1 -v`, before any implementation): 59 tests fail (58 subtests and the Radius test), all for the missing behavior (stubs return not-implemented results, catalog accessors return nil, the flows do not exist). No panics.

| test | failing subtests |
|---|---:|
| `TestTypesafeSystemOneUpstream` | 9 |
| `TestClassifierModelsUpstream` | 7 |
| `TestCloudflareWorkersAISystemOneUpstream` | 4 |
| `TestLlamaCppClassifyUpstream` | 15 |
| `TestOAuthCallbackServerUpstream` | 8 |
| `TestWaitForCallbackOrManualInputUpstream` | 4 |
| `TestOpenAIChatGPTOAuthUpstream` | 6 |
| `TestOpenAIResponsesChatGPTSignInUpstream` | 2 (the two "keeps those fields" rows pass: existing behavior) |
| `TestOAuthAuthAdaptersUpstream` | 2 |
| `TestAnthropicUpstreamOAuth` | 1 |
| `TestRadiusOAuthExchangesBrowserCodeBeforeShowingSignInPageUpstream` | 1 |

## Green

Commit `167ecfbef` (`feat(ai): implement the classifiers, the shared OAuth callback server and Sign in with ChatGPT (green)`), then a refactor commit. The ported tests are unchanged except for these upstream-driven or gate-driven edits, each cited in the file:

- `sk-` test keys in `openai_responses_compat_upstream_test.go` and `cache_retention_upstream_test.go` (the 0.99.1 tests use `sk-fake-key`; a non-`sk-` key is now a ChatGPT sign-in token).
- `typesafe` in the `auth_env_keys_test.go` table (`env-api-keys.ts:93`); `openai` in the dual-auth allow list of `api_key_providers_test.go` (`providers/openai.ts:15-18`).
- `anthropic_oauth_exchange_test.go`: 0.99.1 aborts the manual prompt before the exchange notification.
- `llama_cpp_classify_upstream_test.go`: `maps.Copy` (`go fix`); no input or expectation changed.
- `TestOpenRouterCancelWaitRespectsClaim` removed: the internals it drove are the shared callback server now, covered by the ported `TestOAuthCallbackServerUpstream`.

Verification (isolated HOME, PIG_HOME and PIG_CODING_AGENT_DIR; real toolchain on PATH):

- `go test -race ./ai/... ./agent/... ./internal/codingagent/... ./coding/... ./internal/... ./extensions/... ./cmd/...`: green apart from the environment failures below.
- Load: `GOMAXPROCS=4 taskset -c 0-3` with four CPU burners pinned to the same cores, `-race -count=24` over the classifier, callback-server, OAuth and ChatGPT tests.
- Mutation checks (revert, see red, restore): fresh per-attempt timeout, retry classification, timeout message, bool `noul` mapping, Cloudflare route, label-token cache, `/v1` root stripping, readout escalation, reasoning-block closing, callback claimed check, cancel-after-claim, manual prompt cancellation and join, failure page, ChatGPT direct-token scope and expiry margin, `isChatGPTSignIn`, Radius and Anthropic page name, page flush before close. Own regression tests: `ai/classifier_test.go`, `ai/oauth_callback_server_test.go`.
- Source fixes found by gates: `lazyregexp` for the ChatGPT device-ID pattern (`TestLinkedPackagesCompileNoRegexpAtInit`); the login option list honours the composed runtime's auth methods (`internal/codingagent/interactive_auth.go`); the callback result page is flushed before the server closes (Go buffers until the handler returns; Node's `response.end()` does not); the label lookups run unbounded like `Promise.all` (no invented limit).

Environment failures, not from this lane: `internal/experimental` `TestPinnedUpstreamRadiusSource` (no `interface-extractor/node_modules`), `internal/nativeplatform` X11 tests and `TestNodeVendoredTuiUpstreamTests` native-clipboard (no Xvfb). `make parity-family` cannot run in a lane worktree: `parity-deps` refuses to install through the shared `extensions/sdk-ts/node_modules`. The lead runs `FAMILY=oauth` and `FAMILY=ai-sdk` centrally.

## Deferred and for the lead

- `.upstream/current` still pins 0.87.1, so the three new `// upstream:` markers (`system-one-shared.ts`, `llama-cpp-classify.ts`) cannot resolve in `make divergence-guard` until `make upstream-mirror` moves the mirror. `oauthCallbackHeaderTimeout` (10s `ReadHeaderTimeout`; Node has its own default, Go has none) replaces the two stale baseline entries for the OpenRouter and Radius servers with one entry.
- `ai/classifier_models_generated.go` comes from `go run ./cmd/gen-classifier-models -src <pi-ai 0.99.1 dist>/models.generated.js -out ai/classifier_models_generated.go`. `automation/gen/generate-model-catalogs.sh` is untouched; the lead wires the generator in.
- `ClassifierContext.State` is a `JsonObject` (a Go map): multi-key state is rendered in sorted key order, JS uses insertion order.
- Sign in with ChatGPT needs a device ID: `OAuthAuth.Login` and `Models.Login` take `LoginOptions{GetDeviceID}` and `OAuthLoginCallbacks` has `GetDeviceID`. Nothing in the app supplies one yet (settings `getOrCreateDeviceId`, `cmd/pig/auth_commands.go`, interactive login belong to later families), so `/login openai` reports that a device ID is required.
- `ai.BuiltinProviders`, `BuiltinModels` and `BuiltinProvider` are a minimal assembly for the classifier catalog and chat routing. Family 6 may replace them.
- Not in this lane: `model-runtime-classifiers` and `llama-extension` (coding-agent, family 6); `openai-responses-usage-limit` and the retry test (family 2). Shared file with family 2: `ai/openai_responses.go` (API key resolved before the payload is built; `isChatGPTSignIn` omissions).
- `test/parity/testdata/provider-models/pig/main.go` still uses the old `Models []*ai.Model` (family 1 carry-over).
