# ai-rows: ai interface rows from the bottom (Pi 1.0.4)

Base `9e25939e3`. Takes the utils/*, providers/* and member/type-mismatch rows of the ai package; `gap-core` works the top and writes `ai-rule-requests.md`. `make interface-gaps` ai rows: 1748 before, 1719 after. 26 rows left the gap list from the ports below; the rest are rule requests (below), not behaviour.

## Ported (real behaviour or surface that Go lacked)

| Family | Pi source | Go | Evidence (each mutation-checked) |
|---|---|---|---|
| `validateToolCall`, `validateToolArguments` live in `ai` | `utils/validation.ts:153,168` (Pi's agent imports them from pi-ai) | `agent/validate*.go` moved to `ai/validate*.go`; new `ai.ValidateToolCall`; `ai.ValidateToolArgumentsJSON` is the member-order-preserving entry the agent loop uses; `durable/harness` and `agent/tool_execution.go` call it | `TestValidateToolCallFindsTheNamedTool` (message `Tool "x" not found`, first match wins, input not mutated; both mutations fail), the moved validation suites, `TestToolArgumentValidationPiOracle` |
| `StringEnum` | `utils/typebox-helpers.ts:15` | `ai.StringEnum`, `StringEnumOptions` (production caller `internal/evals/evalsuites/audit.go`) | `TestStringEnumMatchesPiSchemas`, `TestStringEnumValidatesAsAToolParameter` |
| generic `EventStream<T, R>` | `utils/event-stream.ts:25-95` | `ai.EventStream[T, R]`; `agent.AgentEventStream` is rebuilt on it (its private queue is deleted) | the five cases of `packages/ai/test/event-stream.test.ts` plus cancel and ordering cases (`TestEventStream*`); three mutations fail (late push, completion, concurrent order) |
| closed discriminators were bare `string` | `api/pi-messages.ts:54` (`PiMessagesEvent`), `types.ts:707` (`ConstrainedSamplingConfig`), `providers/faux.ts:52` (`FauxContentBlock`) | `PiMessagesEventType`, `ConstrainedSamplingType`, `FauxContentBlockType` with constants (wire unchanged) | `TestDiscriminatedUnionTypesMatchTheGoConstants` reads the pinned sources |
| faux blocks lost their signatures | `providers/faux.ts:339-420` (the stream ends with the cloned resolved message) | `FauxContentBlock.TextSignature/ThinkingSignature/Redacted/ThoughtSignature/Namespace`, applied in `applyFauxBlockMetadata` | `TestFauxResponseBlocksKeepTheirSignaturesInTheFinalMessage` (red without the call) |
| OpenCode session header missing from the ai provider | `providers/opencode-headers.ts:19`, `opencode.ts`, `opencode-go.ts` | `ai/opencode_headers.go`; `builtinProvider` wraps `opencode` and `opencode-go` | `TestOpenCodeProvidersMapSessionIDToTheSessionHeader` (six API paths), `TestOpenCodeSessionHeaderYieldsToCallerHeadersAndNeedsASession`; red before. The `coding` layer's own header stays: Pi has both |
| GitHub Copilot `filterModels` missing from the ai provider | `providers/github-copilot.ts:20-30` | `ai/github_copilot_filter.go` | `TestGitHubCopilotProviderFiltersModelsByTheAccountsAvailableModelIDs` (9 credential shapes) |
| built-in providers had no provider-level `baseUrl` | 31 `providers/<id>.ts` (`baseUrl: ...`); read by `core/bug-report.ts:118` and `provider-composer.ts:608` | `builtinProviderBaseURLs`, `ai.ProviderBaseURL`; `BugReportProviderInfo` falls back to it | `TestBuiltinProvidersMatchPinnedProviderModules` (all 31 modules: id, name, baseUrl, API-key label and env names, OAuth presence, chat APIs, parsed from the pinned sources; the base URL part was red for 31); `TestBugReportProviderInfoReportsTheBuiltInProviderBaseURL` (bug report printed `null`) |

Bug found on the way: `agent` and `durable` had no direct test that the order-preserving validation entry is the one the loop calls; `TestValidateToolArgsKeepsMemberOrder` now goes through `ValidateToolArgumentsJSON`.

## Closed by an existing record (no code)

- `ModelsError.stack`, `PiMessagesResponseError.stack` (3 + 1 rows): D103 (`gap-core` branch, commit `3c4cfad99`). Cite D103 on the rows when that branch lands. `ModelsError.name` is already designed-out on `pkg:ai/models#ModelsError::property:name`.
- `getPiUserAgent` (2 rows): D65 (`ai.PiUserAgent`).

## Rule requests (the detector or ledger lane; not behaviour gaps)

| id | rows | request | Go evidence |
|---|---:|---|---|
| RR-A | 18 | `[Symbol.asyncIterator]` is the Go method `Events(ctx) iter.Seq[T]` | `ai/event_stream.go`, `ai/generic_event_stream.go` |
| RR-B | 73 | `<x>Provider()` factory functions in `providers/<id>.ts` are `ai.builtinProvider(id)`; `getBuiltinProviders` is `BuiltinProviders`, `BuiltinProvider` is `ModelsProvider` | `TestBuiltinProvidersMatchPinnedProviderModules` proves each factory's declaration |
| RR-C | 6 | `api/cloudflare-ai-binding.ts` (`AiBinding`, `createAiBindingFetch`, the auth sentinel) is the Cloudflare Workers runtime binding `env.AI`; a Go process has none: designed-out as a runtime mechanic | none needed |
| RR-D | 10 | `abort.ts` / `abort-signals.ts` (`operationSignal`, `raceWithAbortSignal`, `combineAbortSignals`, `CombinedAbortSignal`) are `context.Context` (`context.WithCancelCause`, `context.AfterFunc`, `select`); `sleep` is `abortableSleep` (`ai/provider_retry.go`) | `ai/models_runtime_signal.go`, `ai/provider_retry.go` |
| RR-E | 2 | `retryProviderRequest` is the transport retry (`ai/provider_retry.go`, `WithProviderRequestRetry`) | `ai/provider_retry*_test.go` |
| RR-F | 4 | `validateToolArguments`/`validateToolCall` result `any` is `map[string]any` (T4), `StringEnum` options bag `{ description?, default? }` is `*StringEnumOptions` (T12) | `ai/validate_arguments.go`, `ai/typebox_helpers.go` |
| RR-G | 2 | `TranscriptMessages` is `[]Message`; `TranscriptContext` is the branded wrapper (A3) | `ai/transcript.go` |
| RR-H | 3 | `ClassifierChoiceQuestion.criteria` and `ClassifierChoiceAnswer.probabilities` are ordered slices: a `Record` iterates integer-like keys first, so a slice keeps the author's order; no divergence for non-numeric keys |  |

## Gates

`go vet ./ai ./agent ./durable/... ./internal/codingagent ./internal/evals/...`; `go test ./agent ./durable/harness ./internal/evals/... ./internal/codingagent -run TestBugReport`; `go test ./ai`: 17 failures, all environmental (Pi oracle scripts cannot resolve `extensions/sdk-ts/node_modules`, `.upstream/v1.0.4` is absent). `make generate`, `make interface-gaps-update`.


## Round 2 (after merging integrate-042 at 353 commits)

integrate-042 already held the same ports as round 1 for `StringEnum`, the OpenCode session header, generic `EventStream`, `PiMessagesEventType` and the faux tool-call members, so those files take integrate's version and the duplicates are deleted. What stays from round 1: validation in `ai` (integrate had dropped `ValidateToolCall`), Copilot `filterModels`, provider `baseUrl` plus the 31-module differential, `FauxContentBlockType` and the text/thinking signatures on faux blocks.

| Family | Pi source | Fix | Evidence |
|---|---|---|---|
| custom `thinkingBudgets` ignored for budget-based Claude on Anthropic and Bedrock | `api/anthropic-messages.ts:928-973`, `api/bedrock-converse-stream.ts:531-580`, `api/simple-options.ts:81-105` | `thinkingToAnthropicConfig` takes the custom budgets (`ThinkingBudgetForLevel`); Bedrock sends `adjustMaxTokensForThinking`'s clamped `maxTokens` and `min(budget, maxTokens - 1024)` (`adjustBedrockBudgetThinking`). Before: Bedrock sent `maxTokens: 4096` with a 16384 budget, where Pi sends 20480. | `TestAnthropicStreamSimpleThinkingBudgetsMatchPi` (3 red cases) and `TestBedrockStreamSimpleThinkingBudgetsMatchPi` (8 cases through `StreamSimple`; the cap/budget cases fail without the Bedrock `maxTokens` adjustment) |
| renames the detector could not guess | `llama-cpp-classify.ts`, `typesafe-system-one.ts`, `cloudflare-workers-ai-system-one.ts` | `renames.json`: `labelProbabilities`, `llamaServerRoot`, `peakConfidence` and the three `classify` functions map to `LlamaCpp*` / `Classify*` (12 rows) | `make interface-gaps` |
| compat and root re-exports follow the module that defines them | RR3 | 32 renames copied from the defining module (`GoogleOptions` and the other per-provider options to `StreamOptions`, faux handle members, `createFauxCore`); the two stale `agent/validate_arguments.go` renames point at `ai/` | `make interface-gaps` |

ai rows: 1382 after the merge, 1116 now. Not closed on purpose: the legacy `compat` registry and `stream*` functions (80 rows; shard 4 deleted them as caller-free, an owner decision), `OpenRouterRouting` and `VercelGatewayRouting` (kept as verbatim `map[string]any`, which forwards unknown keys), `api/openai-responses-shared` and `google-shared` helpers (the Go provider keeps that logic inside its methods; splitting it only for shape would add duplicates), `cloudflare-ai-binding` (RR-C).


## Round 3

| Family | Fix | Evidence |
|---|---|---|
| per-provider options types (`AnthropicOptions`, `AzureOpenAIResponsesOptions`, `BedrockOptions`, with their `compat` and `api/*` copies) are `ai.StreamOptions` (LEAD-ANSWERS-ledger 2); `RegisterFauxProviderOptions.provider` is `FauxConfig.ProviderID`; `FauxProviderRegistration` is `FauxProviderHandle` | `renames.json`; members then resolve by json tag | `make interface-gaps` |
| `FauxProviderRegistration.api` / `FauxProviderHandle.api` had no Go member | `FauxProviderHandle.API()` | `TestFauxRegistrationContractUpstream` (explicit and random ids equal the models' API) |
| stale `reviewed-gaps.json` entries after integrate's rules decided them | removed | `make interface-gaps` |

ai rows 979 to 747. Open and not behaviour: per-provider `toolChoice` unions (T10/U1: one `any` field serves every provider's union; needs a detector rule), `AnthropicOptions.client` (an SDK client object; Go has no Anthropic SDK client), `RegisterFauxProviderOptions.tokenSize` (`{min, max}` against `MinTokenSize`/`MaxTokenSize`), the `compat` registry (owner decision), P1 not-exercised rows.


## Round 4

Merged integrate-042 (7 commits; another lane had added the same `FauxProviderHandle.API()`, theirs kept). `ImageModel.type` and `ClassifierModel.type` are the `ModelType()` methods (renames for the root and `compat` rows; discriminator test `TestModelTypeDiscriminatorsMatchUpstreamLiterals`). ai rows 741 to 733. Proposal for the owner: design out `AnthropicOptions.client` (a pre-built Anthropic SDK client object; Go has no such SDK client) with the other runtime-object rows of RR-C. `StreamOptions.TelemetryContext` is carried but never read by Pi's ai package either (`types.ts:137`, `simple-options.ts:48` only), so its not-exercised row belongs to rule P1.


## Round 5: remaining non-P1 rows are rules, not behaviour

Merged integrate-042 again (no ai conflicts). The remaining undecidable and member-missing rows outside `compat` and the root re-exports are shape rules. None hides missing Pi behaviour:

| Rows | Why the detector cannot decide | Proposed rule |
|---|---|---|
| `*Options::property:toolChoice` (Anthropic, Azure, Bedrock, Mistral, OpenAI Responses, PiMessages), `serviceTier` | T10/T9: one `any`/string field in `StreamOptions` carries each provider's union; each provider's wire builder validates its own literals | a union field mapped to a Go `any` or string by json tag is closed when the provider builder handles every upstream member |
| `fauxText`/`fauxThinking`/`fauxToolCall`, `FauxProviderHandle.models`, `tokenSize` | T9t/T12: Go `FauxContentBlock` is one struct for the three upstream variants; `tokenSize` is `MinTokenSize`/`MaxTokenSize` | struct-for-union and object-for-two-fields closure |
| `validateToolArguments`/`validateToolCall` result, `StringEnum` options | T4/T12: upstream `any` and `T[number]` | `any` result against `map[string]any` where Pi validates an object schema |
| `[Symbol.asyncIterator]` | T9: `iter.Seq` is the Go iterator | language mechanic (`docs/typescript-to-go-porting.md`) |
| `Models.getAuth(model)` call-1 rows | held by `reviewed-gaps.json` "port-needed"; Go has `GetAuth(providerID)` and `GetModelAuth(model)` | owner decision: delete the reviewed entries, then the `GetModelAuth` renames apply |
| `AnthropicOptions.client`, `AiBinding.aiGatewayLogId` | runtime SDK object, type-only member | design out |


## Round 6

Merged integrate-042 (75 commits) and removed 5 more stale `reviewed-gaps.json` entries that integrate itself fails on. ai rows 736 to 722. The new undecidable/signature rows are shape families, with no missing behaviour:

| Rows | Finding | Proposed rule |
|---|---|---|
| `api/*#stream::call:0` (10 provider modules) `context: T12: ... transcriptContextBrand` | `ai.Context` is the Go form of `TranscriptContext`; the brand is a TypeScript-only nominal marker | brand-only symbol keys are not members |
| `api/google-shared#mapToolChoice`, `resolveGoogleFunctionCallingMode`, `mapStopReason`, `mapStopReasonString`, `getDisabledGoogleThinkingConfig`, `toGoogleSdkThinkingLevel`, `convertTools` | Go inlines them in the request builder (`ai/google.go` around line 820, `mapGoogleFinishReason`, `geminiConvertTools`); behaviour was checked against `google-shared.ts:410-486`: explicit none/any beat strict `VALIDATED`, an unknown non-empty choice maps to AUTO, absent stays unset | none: extracting them only to match names would add duplicates (round 1 decision) |


## Round 7

Integrate-042 had 2 new commits (no ai changes). `Model` members `api`, `baseUrl`, `headers`, `compat`, `reasoning`, `contextWindow`, `maxTokens` and `name` are renamed to the Go fields that hold them (`ProviderMetadata.API`, `.BaseURL`, `.Headers`, `.Compat`, `.Reasoning`, `ModelCapabilities.ContextWindow`, `.MaxOutputTokens`, `Model.DisplayName`) for the root and `compat` rows. ai rows 722 to 708. Left: `Model.cost` (Pi's `{input, output, cacheRead, cacheWrite}` object against four flat `*CostPer1M` fields plus `CostTiers`), `Model.compat` T9 (`OpenAIResponsesCompat` against `ModelCompat`), `Model.provider` (`string` against `ai.Provider`): shape rules.


## Round 8: Radius provider shape (owner decision, not ported)

`providers/radius#radiusProvider` and `providers/all#radiusProvider` return Pi's `Provider<"pi-messages">` (id, name, `auth`, `getModels`, `refreshModels`, `stream`, `streamSimple`). Go's `ai.RadiusProvider` is a separate type that `internal/codingagent` drives itself (`ModelRegistry.radius`, `configureRadiusProvidersLocked`, `RadiusProviderAuth`, its own refresh path). A faithful port makes `NewRadiusProvider` return a `*ai.ModelsProvider` (auth from `RadiusProviderAuth`, `GetModels` merging baseline and refreshed models, `RefreshModels` with the stored/legacy/network steps of `radius.ts:46-93`, streams delegating to pi-messages) and moves the coding-agent registry onto it. I did not add that provider beside the existing path: it would have no production caller (AGENTS.md verification rule 8), and the rewiring crosses into the coding-agent model registry that other lanes own. Needs an owner decision on who rewires `internal/codingagent/radius_models.go`, `registry_request_auth.go` and `request_auth_runtime.go`.


## Round 9

Merged integrate-042 (118 commits). Conflicts: `ai/types.go` (integrate landed the same `ConstrainedSamplingType`/`ConstrainedSamplingStrict` discriminators; theirs kept) and `agent/validate_arguments.go` (integrate added `agent.ValidateToolCall` after a rule request placing validation in `agent`; my branch had moved validation into `ai`, which is Pi's `utils/validation.ts` location, with `ai.ValidateToolCall`). I kept the single `ai` implementation, deleted the agent copy, and folded integrate's table-driven `TestValidateToolCallPicksTheNamedToolOrFailsLikeUpstream` into `ai/validate_tool_arguments_test.go`. Both run against `ai`. ai rows 707 to 706. The agent loop and `durable/harness` already call `ai.ValidateToolArguments*`; `go test ./agent ./durable/harness ./ai` passes.


## Round 10

Merged integrate-042 (83 commits; generated files taken from theirs, `renames.json` unioned with my `pkg:ai` entries, 3 more stale reviewed gaps dropped). ai rows 704 to 705. The new reviewed rename `ai:ApiOptionsMap` to `ai/types.go#StreamOptions` (`renames-reviewed.json:5`) turns Pi's type-level map from API id to options type into 20 `member-missing` rows (`ApiOptionsMap::property:<api id>`, root and `compat`), because `StreamOptions` has no field per API id. `ApiOptionsMap` carries no runtime value in Go: every API reads `StreamOptions` by json tag (LEAD-ANSWERS-ledger 2). Those rows need the reviewed rename replaced by a rule (type-level lookup map, like `ModelTypeMap`) rather than a field per API.

## Round 11: compat streamSimple and createFauxCore (3 claimed rows, 8 ledger rows with children)

- `pkg:ai/compat#streamSimple` (+`::call:0`): compat.ts:278-292 takes the public `Context`, normalizes it, then dispatches to the registry's `streamSimple`. Go's `ai.StreamSimple` is the API module's `streamSimple` over an already normalized transcript, so the name rule bound the wrong function. Ported the missing public form as `ai.StreamSimpleCompat` (ai/compat.go), which `CompleteSimple` now calls (production call site, as compat.ts completeSimple calls streamSimple), and renamed the two ids to it. `TestStreamSimpleCompatNormalizesTheContextAndUsesTheSimpleImplementation` fails when `StreamSimpleCompat` dispatches the non-simple implementation (mutation-checked).
- `createFauxCore::call:0` (root, compat, providers/faux): the Go `CreateFauxCore` already exists (ai/faux_core.go) with every member of Pi's returned literal; the detector's T12 check reports the overloaded `getModel` pair against the one variadic `GetModel(id ...string)` as undecidable. Three reviewed-types entries (reason cites faux.ts:663-675 and `TestCreateFauxCoreGetModelOverloads`).
- ai ledger rows 15 -> 7. The remaining ai rows are `convertMessages` (google-shared), `BuiltinImageProvider` and the Radius provider.

## Round 12: behaviour proofs for four tool-proof-only rows
- `openai-responses-shared#ConvertResponsesMessagesOptions.toolOptions` (Go `ConvertResponsesMessagesOptions.ToolOptions`): `TestConvertResponsesMessagesMatchesPi` already runs Pi's own `convertResponsesMessages` in Node over the same inputs (cases "additional tools take toolOptions strict true" and "tool search output takes toolOptions strict null", openai-responses-shared.ts:131,191,210). Mutation: `systemToolAdditions` using a zero `ConvertResponsesToolsOptions` fails both cases. No new test needed.
- `openai-responses.lazy#openAIResponsesApi` (Go `OpenAIResponsesAPI`): the existing oracle test only covers the setup-failure stream, which the Azure API produces too (swapping `OpenAIResponsesAPI` to the Azure streams still passed). New `TestOpenAIResponsesAPIStreamsThroughTheOpenAIResponsesEndpoint` drives a hermetic Responses SSE through it and asserts the `/v1/responses` POST with the bearer key, the request model and `stream`, the streamed text and the usage. Mutations: Azure streams (no bearer header) and completions streams both fail.
- `system-one-shared#SystemOneTransport` / `SystemOneWireRequest`: `TestSystemOneTransportAndWireRequestDriveClassifySystemOne` kills mutations of the `noul` rename, the api check, the dropped questions, the ignored `Output` and the dropped usage, but `transport.URL` could be replaced by a constant. It now asserts the posted URL (system-one-shared.ts:119); that mutation fails.

## Round 13: `CodemodeSandbox` (Go `codemode.Sandbox`) behaviour proof
No new test was needed: `codemode/sandbox_test.go` and `sandbox_lists_test.go` (about 1000 lines, a port of Pi's codemode host tests over `runtime/host.ts`) drive every public member with Pi's inputs, and each mutation of `codemode/sandbox.go` below fails them. `RegisterTool` accepting a duplicate name fails `TestSupportsRegisterAndUnregisterBetweenExecutions` and `TestSandboxExposesRegisteredToolsAndGlobalsSeparatelyInOrder`; `UnregisterTool` not removing fails the former; `Tools()` or `Globals()` returning nothing fails `TestSandboxExposesRegisteredToolsAndGlobalsSeparatelyInOrder`; ignoring `ExecuteOptions.TimeoutMs` fails `TestTimeoutMessageNamesTheDeadlineInMilliseconds` and `TestExecuteReturnsOnlyAfterTheVMIsClosedForEveryTerminalKind`; a changed `Close` abort message fails `TestCloseAbortsEveryRunningExecutionAndWaitsForTheirVMs` and `TestAbortsViaContextAndCancelsInFlightCalls`; `Execute` after `Close` succeeding fails `TestRejectsExecuteAfterClose`. The row stays marked tool-proof-only only because the ledger tool does not bind these tests to the symbol.

## Round 14: behaviour proofs for three more tool-proof-only rows
- `google-vertex.lazy#googleVertexApi` (Go `GoogleVertexAPI`): no test drove it. New `TestGoogleVertexAPIStreamsThroughTheVertexExpressEndpoint` (ai/google_vertex_api_stream_test.go) streams a hermetic Vertex SSE through it in express mode and asserts the `/v1/publishers/google/models/<model>:streamGenerateContent?alt=sse` POST with the `x-goog-api-key` header and no bearer token, the streamed text and the usage. Mutations to the google-generative-ai, openai-completions and mistral streams all fail it.
- `mistral-conversations#MistralOptions.promptMode` (Go `StreamOptions.PromptMode`): already covered by `TestMistralPayloadModeAndEffortMatchPi` (Pi oracle), `TestMistralRawPromptModeOptionIsSentIndependentlyOfReasoningControls`, `TestMistralPromptModeOptionUpstream` and `TestMistralHTTPTransportUpstream`; skipping the `opts.PromptMode` copy (mistral.go:360) fails all four. No new test.
- `compat#azureOpenAIResponsesApi` (Go `AzureOpenAIResponsesAPI`): the oracle test covers only the missing-auth stop reason. New `TestAzureOpenAIResponsesAPIStreamsThroughTheAzureEndpoint` (ai/azure_responses_api_stream_test.go) asserts `POST /openai/v1/responses?api-version=…` with the deployment name as the request model; mutations to the openai-responses and openai-completions streams fail it.

## Round 15: two more tool-proof-only rows (no new test needed)
- `azure-openai-config#AzureEndpointOptions` (Go `AzureEndpointOptions = StreamOptions`, azure_config.go): the behaviour is the four per-request Azure options (`azureApiVersion`, `azureResourceName`, `azureBaseUrl`, `azureDeploymentName`) outranking the `AZURE_OPENAI_*` variables, with `options.Env` scoping the variables (azure-openai-config.ts:7-26, 27-110). Existing tests (`TestResolveAzureConfigPrecedence`, `TestResolveAzureDeploymentName*`, `TestResolveAzureBaseURL*`, `TestAzureBaseURLNormalizationMatchesPi` (Pi oracle) and the per-request provider test `TestAzureEndpointOptionsApplyPerRequestToBothAzureProviders`) kill every mutation tried: ignoring `AzureDeploymentName` (4 tests fail), ignoring `AzureBaseURL` (4), ignoring `AzureResourceName` (3), ignoring `AzureAPIVersion` (6), and reading `AZURE_OPENAI_API_VERSION` without `options.Env` (3).
- `azure-openai-responses.lazy#azureOpenAIResponsesApi` is the same Go symbol, `AzureOpenAIResponsesAPI`, as Round 14's `compat#azureOpenAIResponsesApi`; `TestAzureOpenAIResponsesAPIStreamsThroughTheAzureEndpoint` is its behaviour proof.

## Round 16: AssistantMessageFrameEncoder and ClassifierAnswer proofs
- `AssistantMessageFrameEncoder` (assistant-message-frame.ts:139-160): existing tests (`TestAssistantMessageFrameEncoderMatchesPiOnAStreamTheLivePartialRunsAheadOf` Pi oracle, the encoder-state, end, scratch and catch-up tests) kill each mutation tried: dropping the terminal-event guard (2 tests fail), done before start (2), a second start (1), error not terminal (1), done not terminal (2), start partial not cloned (4), text-start coverage offset (3). No new test.
- `ClassifierAnswer` (types.ts:697): `AnswerType()` had no caller; changing a type tag survived the whole `Classif` suite. The three `MarshalJSON` methods now write the wire `type` from `AnswerType()` (ai/classifier.go), so the tag is a production path: changing any of the three tags now fails 3-4 tests (the marshal and Pi-oracle ones). Parsing (`classifier_wire.go`) was already killed: zeroing the bool probability or the score confidence fails a test.
- `go test ./ai` fails only the two Pi oracles that need `npm ci` in internal/experimental/protocol/testdata/typebox (`TestTypeboxFormatsMatchPiOnSeededStrings`, `TestValidateToolArgumentsMatchesPiOnSeededSchemas`); they fail identically without this change.

## Round 17: durable HookRunner and MemoryStorage proofs (no new test needed)
- `HookRunner` (durable types.ts:158-163, scheduler.ts:1131-1148; Go `runtimeHooks.Each`, durable/harness/scheduler.go): `hook_runner_test.go` kills each mutation tried. Skipping the invocation-ended check (1 test fails), dropping the report of an ordinary error (3), never propagating once signalled (1), always propagating (3), reversed handler order (4) and not recovering a panicking handler (1).
- `MemoryStorage` (storage-memory.ts; Go durable/storage/memory.go, with the Pi oracle `memory_pi_oracle_test.go` and the conformance suite): killed every mutation of the commit path tried. A non-strict sequence check (2 fail), no global-id check (7), no document-action check (2), no open check on prepare (6), applying a prepared commit twice (1) and not detaching the writes (4).

## Round 18: TranscriptMessages and three compat API entry points (no new test needed)
- `TranscriptMessages` (utils/transcript.ts:35-40; Go `TranscriptMessages[M]` read through `systemMessageOf`, ai/transcript.go): `TestTranscriptHelpersAcceptAnyRoleCarryingMessage` and the agent custom-role tests kill each mutation tried. A pointer system message that is no longer read (2 transcript tests fail), an application message that wraps a system message that is no longer read (19) and a value system message that is no longer read (3).
- `anthropicMessagesApi`, `bedrockConverseStreamApi` and `googleGenerativeAIApi` (Go `AnthropicMessagesAPI`, `BedrockConverseStreamAPI`, `GoogleGenerativeAIAPI`, ai/api_streams.go): the Pi-oracle tests `compat_api_streams_oracle_test.go`, `bedrock_payload_oracle_test.go`, `anthropic_setup_oracle_test.go` and `api_stream_auth_abort_oracle_test.go` drive each entry point. Pointing one at another API's streams fails them: Anthropic to openai-completions (6 tests), Bedrock to anthropic-messages (4), Google generative-ai to google-vertex (6) and to openai-responses (7).

## Round 19: wire:c6319aac (F2) and wire:e1132d87 (F16) are already fixed on integrate
- F2 (native stream callbacks dispatched by name): `nativeStreamCallback` (native_provider.go) switches on the callback method. Disabling the `onResponse` case fails `TestNodeNativeProviderReceivesTheCallersOnResponseAndOnPayloadByName`, `TestNativeProviderSessionRequestHooks` and `TestNativeProviderHostLifecycle`.
- F16 (isolated restart keeps stale commands, shortcuts and flags): `adoptRestartedExtension` (host.go) now calls `ReplaceDeclarations` and `registrationFieldExpectedRed` is empty. Dropping that call fails `TestRegistrationDeclarationsKeepEveryField` and `TestRestartAdoptsTheNewRegistrationsDeclarations`.
- `test/wiring` currently fails `TestScenarioCoversNameRealWireMethods` for `registrations/tool-result-handlers-that-return-nothing-change-nothing` (covers "event tool_result"), a scenario-cover entry from another lane's landing, not from these items.

## Round 20: mistralConversationsApi and openAICodexResponsesApi entry points (no new test needed)
- `mistral-conversations.lazy#mistralConversationsApi` (Go `MistralConversationsAPI`, ai/api_streams.go): pointing it at the openai-completions or openai-responses streams fails 7 tests (the Mistral prompt-mode and payload tests and `api_stream_auth_abort_oracle_test.go`).
- `openai-codex-responses.lazy#openAICodexResponsesApi` (Go `OpenAICodexResponsesAPI`): pointing it at the openai-responses or openai-completions streams fails 5 tests, among them the Pi oracles `TestOpenAICodexResponsesPayloadMatchesPi`, `TestOpenAICodexResponsesResponseMatchesPi` and `TestProcessResponsesStreamEdgeEventsMatchPiOnCodex` and `TestLazyAPIEntryPointsRouteToTheirOwnAPIUpstream`.

## Round 21: finding:54b0005e (a), Codex WebSocket frame key order
- Defect: `startCodexWebSocketAttempt` decoded the request body into `map[string]any` and re-encoded it, so every frame had alphabetically sorted keys, nested objects included. Pi sends `JSON.stringify({ type: "response.create", ...requestBody })` (openai-codex-responses.ts:1542): "type" first, then buildRequestBody's keys in order (:553-564), and a cached continuation is `{ ...body, previous_response_id, input: delta }` (:1470-1475), so input keeps its position and previous_response_id is last.
- Fix: `codexWebSocketFrame` (ai/openai_codex_websocket.go) builds the frame from the body's own bytes, keeping field and nested order, replacing input with the trailing delta items and appending previous_response_id. The decoded maps stay for the continuation comparison, as before.
- Test: `TestCodexWebSocketFramesKeepPisKeyOrder` (ai/openai_codex_websocket_key_order_test.go), red before (sorted keys, "type" last), green after. Mutations: "type" not first (2 Codex tests fail), previous_response_id first (3), full input instead of the delta (3).
- Path (b) (the finding's text is cut off in logs/backlog.tsv; the full text is in handoffs/rev-porter-proof-lead-batch.md): the Anthropic `before_provider_request` replacement. `anthropicWireBody` re-sorted a replacement's keys through `map[string]json.RawMessage`; Pi sends `{ ...nextParams, stream: true }` (anthropic-messages.ts:640-650), which keeps the replacement's order, keeps `stream` in its position or adds it last, and the SDK drops `betas` from the body. `anthropicWireBody` now reads the replacement with `orderedJSONObject` and writes it with the new `writeOrderedEntries` (ai/ordered_json.go); `codexWebSocketFrame` uses the same two helpers. Test `TestAnthropicReplacementPayloadKeepsItsKeyOrder` (json.RawMessage payloads, as the subprocess onPayload returns), red before (sorted keys), green after. Mutations: stream always last (3 fail), stream first (3), stream not forced true (11), betas left in the body (4).

## Round 22: wire:6d3f0532 (F12) is already fixed on integrate
`oauth.cb.onManualCodeInput` now carries the provider object's own manual_code prompt: the bridge (coding/extension/host/subprocess/oauth_bridge.go, CallOAuthOnManualCodeInput) reads `{type:"manual_code", message, ...}` and passes it to `OnManualCodePromptContext`; a legacy `onManualCodeInput()` still asks "Paste the authorization code" (provider-composer.ts:363, interactive-mode.ts:6283-6284). Mutations fail the Manual/OAuth/Login tests: own prompt dropped (3), the manual_code type check skipped (3), the legacy default dropped (2).

Round 21 follow-up: go-idioms landed its own fix for both paths on integrate-042 (d37c76932f, 992f3cbc69). I took its `anthropicWireBody` and `codexWebSocketFrame`, deleted my `writeOrderedEntries` (it had no caller left), and kept `TestAnthropicReplacementPayloadKeepsItsKeyOrder` and `TestCodexWebSocketFramesKeepPisKeyOrder`, which pass against its implementation.

## Round 23: finding:77d26b19, the rest of the Anthropic SDK's beta messages.create on a replacement body
@anthropic-ai/sdk beta/messages/messages.js:24-58 `create()` does more than drop `betas`: `{ betas, user_profile_id, workspace_id, ...body }` moves a non-null user_profile_id and workspace_id to the anthropic-user-profile-id and anthropic-workspace-id headers, and :140-156 `transformOutputFormat` turns a truthy `output_format` into `output_config.format` (merged into an existing output_config in its position, appended last otherwise, and an AnthropicError "Both output_format and output_config.format were provided..." before any request when both carry a format). PiG's `anthropicWireBody` stopped at betas. `anthropicSDKParams` (ai/anthropic_client.go) now applies the rest to a replacement body; the headers reach the HTTP request (`applyAnthropicRequestHeaders`) and an injected client (`AnthropicCreateParams.UserProfileID/WorkspaceID`), and the SDK error is the stream's error message unwrapped.
Tests (ai/anthropic_sdk_params_test.go), red before (ids stayed in the body, no headers, output_format sent as is, no error): TestAnthropicReplacementUserProfileAndWorkspaceBecomeHeaders, TestAnthropicReplacementNullProfileIDsSendNoHeader, TestAnthropicReplacementOutputFormatBecomesOutputConfigFormat (4 cases), TestAnthropicReplacementWithBothFormatsFailsBeforeTheRequest. Mutations: no profile header, wrong workspace header, output_config first, format not merged over a falsy one, no both-formats error, falsy values treated as truthy, workspace_id left in the body each fail 1-2 of them. Verified against the SDK source in extensions/sdk-ts/node_modules (no live Pi oracle).

## Round 24: finding:e9504bc2, a raw onPayload replacement is written as JSON.stringify writes it
`marshalRequestBody` copied a `json.RawMessage` replacement (what the subprocess onPayload returns) byte for byte, so `\u00e9`, `1.0` and `1e21` reached the wire as typed. Pi's hook returns a parsed value and the SDK JSON.stringify's it (openai 7.19.0 internal/request-options.js:29), so `é`, `1` and `1e+21`. `rawRequestBody` (ai/request_body_raw.go) now runs `jsonstringify.Canonicalize` on a RawMessage body in both `marshalRequestBody` variants (json v1 and v2), so every provider that marshals a replacement is covered, and `anthropicWireBody` reads the canonical text.
Test `TestRawReplacementBodiesAreWrittenAsJSONStringifyWritesThem` (ai/request_body_raw_replacement_test.go): escapes, `1.0`, `1e21`, `-0`, `1.50`, a repeated key and a lone surrogate, through `marshalRequestBody` and `anthropicWireBody`. Red before (text copied as typed), green after under both the default build and GOEXPERIMENT=jsonv2. Mutations: verbatim copy (3 fail), call removed from the v2 variant (3), from the v1 variant under GOEXPERIMENT=nojsonv2 (3).

## Round 25: durable Storage.entry (VisibleEntry) and StorageReadBenchmark
- `Storage.entry(conversationId, id, context)` (types.ts:1038; Go `VisibleEntry`, durable/storage/memory.go): the visibility range, the commit sequence and the clone were already killed (range off by one 12 tests, commitSeq zero 7, nil entry 2, no clone 1). Dropping the `assertOpen` call survived: memory.ts:476 asserts open in both overloads, and a closed store without it fails only with "Unknown conversation". New `TestClosedStorageRefusesBothEntryLookups` (durable/storage/memory_closed_entry_lookup_test.go) asserts the closed-storage error of `Entry` and `VisibleEntry`; dropping either assertOpen now fails it.
- `StorageReadBenchmark` (testing/storage-benchmark.ts:278): `TestStorageBenchmarkScenariosReturnWhatTheyExpectOnEveryBackend` kills a wrong lookup result (4), a wrong expected value (4), a wrong page size (4) and a changed scenario name (1). No new test.

## Round 26: Agent.clearFollowUpQueue proof
`Agent.clearFollowUpQueue` (agent.ts:313-316; Go `ClearFollowUpQueue`, agent/agent.go:1601) clears the follow-up queue and nothing else. The Pi-oracle and queue tests (agent_class_pi_oracle_test.go, clear_queues_test.go, clear_all_queues_*_test.go) kill each mutation tried: a no-op (5 tests fail), clearing the steering queue instead (5) and clearing both queues (3). No new test.

## Round 27: durable JsonlStoragePoisonedError
`JsonlStoragePoisonedError` (storage.ts:88-95; Go durable/storage/jsonl/storage.go) and its poisoning (`poison`, storage.ts:836-839, `assertUsable` :841-844): existing tests killed a changed message, a changed `name`, a dropped cause and a main-file append that does not poison (1, 1, 2, 1 tests fail). One mutation survived: `poison` replacing an existing poison instead of `??=` keeping the first. New `TestJsonlStoragePoisonKeepsTheFirstFailure` (durable/storage/jsonl/poisoned_error_test.go) asserts a second failure returns the first error with the first cause and that `assertUsable` keeps returning it; the replacing mutation now fails it.

## Round 28: wire:e8a6c64b (F13) and wire:f099c169 (F5-F7) already resolved on integrate
- F13 (registerProvider with refreshModels dropped the D36 Insecure opt-in): `ProviderConfigInput.Insecure` carries it, `ProviderInsecure` reads the composed native input (internal/codingagent/registry_request_auth.go), and a re-registration keeps an earlier opt-in (native_model_runtime.go:179). `TestRegistrationWithRefreshModelsKeepsItsTLSOptIn` and the coding refresh tests fail 4 tests each when the native-input read is dropped, the merge replaces instead of keeping, or the namespace conversion drops Insecure.
- F5 (provider_superseded): the Go, Rust and Python SDKs keep the callbacks the host's merged registration still calls (model-runtime.ts:921-940), each with a test. F6 and F7 (reload_started, runtime_input_end) drive waits only a Node realm has; `TestNodeOnlyNotificationsReachOnlyNodeRuntimes` pins that the host sends them to Node runtimes only.

## Round 29: system-one-shared SystemOneTransport / SystemOneWireRequest re-proven
Re-ran on the current tree (system-one-shared.ts:119 URL, :17-60 wire shapes; Go ai/system_one.go ClassifySystemOne): `TestSystemOneTransportAndWireRequestDriveClassifySystemOne` and the classifier tests fail every mutation tried: a constant URL (8 tests), a dropped API check (4), dropped usage (7), ignored `Output` (1), dropped questions (2), dropped state (5) and a dropped error label (2). No new test.

## Round 30: ExtensionSelectorComponent proof
`ExtensionSelectorComponent` (extension-selector.ts:19-116; Go tui/extension_selector.go) is driven by tui/extension_selector_*_test.go, extension_dialog_*_test.go and the select-page tests. Every mutation tried fails tests: no clamp at the top (2) or bottom (2), dropped `k` (2) and `j` (10), dropped `\n` confirm (2), confirming an empty selection (3), dropped `app.tools.expand` (3), unstyled selected (3) and unselected (3) rows, a countdown without a TUI (1), the first countdown tick requesting a render (1), a changed hint separator (4) and countdown title format (5). No new test.

## Round 31: AgentSession.createReplacedSessionContext
`Session.CreateReplacedSessionContext` (agent-session.ts:4375-4383; Go coding/session_replaced.go). The existing tests killed the options being dropped, the runner's command context being swapped for a fresh runner's, and the await. Four mutations survived: dropping the context from `SendCustomMessage`, from `SendUserMessage`, from the command context (`WithCommandContext`), and always creating a new runner. New tests in coding/session_replaced_context_lifetime_test.go: `TestCreateReplacedSessionContextFollowsTheRequestLifetime` (cancelling the lifetime stops a turn that either send started, and the command context's `WaitForIdle` returns `context.Canceled`) and `TestCreateReplacedSessionContextKeepsTheBoundRunner` (the bound runner survives; a Session without one gets one, reused by the next call). All four mutations now fail them.

## Round 32: BorderedLoader
`BorderedLoader` (bordered-loader.ts:8-67; Go tui/bordered_loader.go): the existing tests kill eleven mutations (default cancellable, border/spinner/message colours, `onAbort` forwarding, the cancel hint's spacer and key, `HandleInput`, `Dispose` and `Stop`, the spinner tick: 1-4 tests each). One survived: the nil-theme fallback to `ActiveTheme()`, a Go convenience with no production caller (Pi's theme argument is required). Review (lg-review-i4) deleted the fallback instead of testing it, so the constructor takes a required theme as in Pi.

## Round 33: Agent.onPayload (SetBeforeProviderHook)
`Agent.onPayload` (agent.ts:198, :238, :473; Go agent/agent.go `SetBeforeProviderHook`, `AgentOptions.OnPayload`, agent_loop.go createLoopConfig): the existing tests kill a no-op setter, an options hook that is not carried and a loop that does not pass the hook to the stream (3 tests each; `TestAgentClassMatchesPi` and `TestAgentLoopMatchesPi` fail without any mutation in this tree because the Pi oracle needs `npm ci`). One mutation survived even under `-race`: the setter storing the hook without `stateMu`. New `TestSetBeforeProviderHookIsSafeWhileRunsCaptureIt` (agent/runtime_hook_setters_test.go) assigns the hook while runs capture it; under `-race` the unlocked setter fails it (2 findings).

## Round 34: BedrockCompat.supportsStrictMode
`BedrockCompat` (types.ts:978-982; bedrock-converse-stream.ts:250 `model.compat?.supportsStrictMode ?? false`, :1151-1157): the Pi payload oracle and `TestBedrockConvertStrictTools` kill a never-strict and an ignored-in-convert mutation, but a Stream that always passed `supportsStrictMode=true` survived because the oracle only sets a constrained-sampling tool for strict models. New `TestBedrockStrictToolSpecFollowsTheModelCompat` (ai/bedrock_strict_compat_test.go) drives Stream against a capture server for no compat, `false`, an empty compat and `true`; always-strict (3 subtests fail), never-strict (1) and default-true (2) now fail it.
