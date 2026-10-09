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
