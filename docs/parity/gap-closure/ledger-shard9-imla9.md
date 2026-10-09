# Shard 9 (top-down, lane lg-imla-9): what closed and what is a rule request

State at 9e25939e3 (integrate-042), 220 shard-9 rows in `ledger-resplit.tsv`, 193 still open in `make interface-gaps` before this lane.
No `lg-help-9` lane exists, so this lane also covers the bottom half of the shard.
No lane branch (`lg-rules`, `lg-ai-nogo-a/b`, `lg-ca-member-a/b`, `lg-ca-rest-a`, `lg-tui-a/b`, `lg-help-*`) is merged into integrate-042 yet, and most of the open shard-9 rows are exactly what those branches close or file as rule requests. A merge of `lg-rules` into a scratch tree closes 11 more shard-9 rows (193 to 182) and leaves the groups below.

## Closed here

| Group | Cause | Fix |
|---|---|---|
| `agent/.#runToolCall` and `::call:0` | A stale hold: `holds.json` said `RunToolCallOptions` had no `assistantMessage` or `context`, but `agent/run_tool_call.go` has both and passes them to the hooks through `WithToolCallHookContext`. | Hold released. Mutation check: dropping the `WithToolCallHookContext` call fails `TestRunToolCallGivesHooksTheAssistantMessageAndContext` and `TestRunToolCallKeepsTheInheritedHookContext`. |
| `coding-agent/.#InteractiveMode::property:renderInitialMessages` and `::call:0` | Pi's `renderInitialMessages` (interactive-mode.ts:4168-4182) was inline in `Run`. | `InteractiveMode.renderInitialMessages` (interactive_transcript.go), called by `Run`; `TestRenderInitialMessagesPaintsTheTranscriptThenTheTrustWarning`. Mutations: dropping the warning call and moving it before the entries both fail it. |

## Already in flight on other lanes (do not port again)

- `mcp.McpHttpError` stack, name, cause, constructor: lg-help-10 e92900010.
- `codemode.renderToolSample` hold (R3, malformed `$ref` encoding): lg-ca-rest-a eeb5ee160 and lg-ai-types-a. The hold entries for `renderDeclarations`, `renderToolSample`, `renderToolSignature` and `schemaToType` in `holds.json` go with that merge.
- `LazyOAuth`: lg-ai-types-a. `CombineAbortSignals`, `ServerError.Name`, the Cloudflare binding sentinel and the per-API stream accessors were added and then removed as caller-free by b35bc489c.
- Provider factories (`anthropicProvider`, `fauxProvider`, `minimaxProvider`, `qwenTokenPlanProvider`, `xiaomiTokenPlanSgpProvider`, `BuiltinProvider`): lg-ai-nogo-b and lg-ca-member-b.
- `Loader.setIndicator` (`LoaderIndicatorOptions`) and `CancellableLoader.handleInput` contract: lg-help-3 batch 1 and lg-help-1.
- `AgentOptions.initialState`: lg-tui-b ("AgentInitialState / AgentOptions.InitialState seed the state as agent.ts:82-100").
- `ServerError.code` typed `ServerOperationErrorCode`: lg-ca-member-b.

## Rule requests for lg-rules (shard 9 remainder)

Each item names the Go counterpart, so a rule or a reviewed rename row can close it without a new member.

### Placement and names (Go has it)
- `getAgentDir` is `internal/codingagent/paths.go#AgentDir` (N5 lists unrelated candidates).
- `parseFrontmatter` is `internal/codingagent/frontmatter#Parse`. `stripFrontmatter` is `frontmatter.Strip`.
- `withFileMutationQueue` is `internal/codingagent/tools/mutation_queue.go#FileMutationQueue.With`. The durable copy is `durable/tools/file_mutation_queue.go`.
- `isThinkingPart` is `ai/google.go#isThinkingPart` (unexported function, N1 should find it) and `clampThinkingBudgetToAnswerRoom` is in `ai/simple_options.go` with the upstream name in its doc comment.
- `ModelRuntimeAuthOverrides` is `ai.AuthResolutionOverrides`. The `getAuth(model)` overload is `RequestAuthRuntime.GetAuth` plus `ModelRegistry.configuredModelHeaders`.
- `durable/testing#STORAGE_MEMORY_SCALES`, `storageBenchmarkPrimaryRecordCount`: `durable/durabletest/storage_benchmark.go` (package `testing` places in `durabletest`).
- `durable/env#err` is `durable/env/env.go#Err`. `ExecutionEnv` members are `FileSystem` and `Shell` members of the same file.
- `Model.api, baseUrl, compat, headers, reasoning` are `Model.ProviderMeta.{API,BaseURL,Compat,Headers,Reasoning}`. `contextWindow, maxTokens, cost` are `Model.Capabilities.{ContextWindow,MaxOutputTokens,InputCostPer1M,OutputCostPer1M,CacheReadCostPer1M,CacheWriteCostPer1M,CostTiers}`. `name` is `Model.DisplayName`. `provider` is `ai.Provider`, a named string type (T1: a named string type is a string).
- `OverlayOptions.col/row/width/maxHeight/margin` are unexported fields behind the `With*` builders (`overlaySize`, `overlayMargin`); `SizeValue` is `number | "N%"`.
- `renderLatex`, `RenderLatexOptions` are `internal/latex#RenderLatex`, `RenderLatexOptions`.
- `ai/utils/sleep#sleep` is `ai/provider_retry.go#abortableSleep` (three unexported variants exist; the context argument is the signal).
- `CombinedAbortSignal`: Go merges cancellation with `context` (`context.WithCancelCause`, `context.AfterFunc`); its only Pi caller is `openai-codex-responses.ts:398`.

### Representation (T9/T10, A3/A4 unions and aliases)
- `AgentOptions.beforeToolCall` and `afterToolCall` (M3): Go keeps ordered hook slices so extensions and the session compose several hooks; Pi has one function.
- `FauxResponseStep`, `ModelsClassifierOptions`, `ModelsDeferredFetchOptions`, `AssistantMessageFrame`, `JsonAgentSessionEvent`, `ReadonlyFooterDataProvider`, `ToolCallEvent`, `CacheWarmingMode`, `TaskState`, `TaskDocFamilyToken`, `AgentEvent`, `SnapshotEvent`, `UsageState`, `InboxState`, `CompactionStatus`, `ConversationStreamOptions`, `PromptSection`, `DocumentPoint`, `JsonValue`, `Seg`, `RemotePlatform`, `TelemetryAttributeDefinition`, `AttachmentEnvelope`, `ServerMessage`, `WireServiceMemberSnapshot`: intersection, union or object-literal aliases (A3/A4); the Go type named in the ledger row is the sealed interface or struct.
- `SimpleStreamOptions.deferred` (`boolean | { window }`) is `ai.DeferredOption`; `toolChoice` is `any` holding `ToolChoice`.
- `Models.getAuth(model: AnyModel)`, `getAvailableOfType`, `getModelOfType`, `getModelsOfType` (`ModelTypeMap[TType]`), `getImageModels` (`BuiltinImageModel`): generic return types and an `AnyModel` union that Go spells as `string` provider id, `*Model` and `ImageModel`.
- `BeforeAgentStartEventResult.message` (`CustomMessage`) is `extension.CustomMessageRef`; `Theme.colors` (`Color`) is `string` ANSI; `showNewVersionNotification(LatestPiRelease)` is `*BinaryUpdate`; `refreshAuthorization(options: TokenRequestOptions)` is `oauth.RefreshAuthorizationOptions`; `UnauthorizedContext.fetch(url, init)` is `McpFetch.Do(*http.Request)`; `DocumentObserver.watchDoc` overloads (six call signatures) are `WatchDocErased` plus the generic `WatchDoc` function (Go methods cannot be generic).
- `ExecutionEnv` `Result<T, FileError>` is `(T, error)` (S5, T9); the leading `context.Context` is Pi's `signal`/`Context` parameter (S4); `StreamDecoder.decode(Uint8Array)` is `[]byte`.
- `compact(...)` takes 11 parameters upstream: `apiKey`, `headers` and `env` are folded into `SimpleCompleter`, `retry` and `callbacks` into `RetryOptions`, `signal` is `ctx`.
- `InteractiveMode` constructor (two parameters upstream: runtime host and options) is `NewInteractiveMode(InteractiveModeOptions)`; `getUserInput()` (`Promise<string>`) is a receive channel; `stop()` and `init()` are the lifecycle inside `Run` and its defers (Run owns raw mode, renderer teardown and the input pump as one scope, so there is no separate caller for either).
- `CancellableLoader` constructor has five parameters upstream (`ui`, two color functions, message, indicator); Go takes colors and frames as strings and has no `ui`. `setIndicator(options)` is `Loader.SetIndicator(frames, interval)` until lg-help-3's `LoaderIndicatorOptions` merges.
- `Theme` constructor and `sourceInfo`: Go `tui.Theme` is a resolved struct of ANSI strings built by the theme loader; Pi's class builds ANSI from `fgColors/bgColors` maps. Resource source info lives in `InteractiveMode.resourceSourceInfo`.
- `isEditToolResult`: the guards `isBash/Read/Edit/Write/Grep/Find/LsToolResult` were added and removed as caller-free (b35bc489c, aa9f1051b); Go code compares `ToolName`. Request: designed-out as a language mechanic (a type guard has no Go use).

### Library reachability (P1: used only by tests, conformance suites or benchmarks)
`durable.Storage` members (`Conversation`, `Entry`, `FindDocument`, `FindLatestHeadMarker`, `ScanConversations`, `ScanDocuments`, `ScanEntries`, `ScanTasks`, `Submission`, `SubmissionByRequest` and their calls), `FileSystem.Id/JoinPath/AbsolutePath/CreateTempDir/CreateTempFile/Watch/Cleanup`, `ReplicatedStateSource.Attach`, `TelemetryAttributeMetadata.cardinality` and `sensitive` (declaration-only metadata, no Pi runtime reads them: telemetry/src/index.ts:30-31), `ServerDrainingError.name/stack/cause`. The binary does not reach `durable`, `chord` or the experimental server as a library; lg-help-1 filed the durable rule (D1-D4).

## Update after merging integrate-042 at 16187f877 (lg-imla-9, batches 2-4)

Open shard-9 rows: 193 (start) -> 147 -> 139 -> 83 -> 77 after the lane merges (compat registry, durable env, OverlayOptions renames) and these ports. 77 rows remain, 10 of them child rows.

Ported here, each against the installed pi-ai or pi-tui:
- `ai/utils/sleep#sleep` and `CombinedAbortSignal` (`ai.Sleep`, `ai.CombineAbortSignals`; kimi refresh and Copilot retry wait through Sleep): a428d04c3.
- `Loader`/`CancellableLoader` constructor indicator argument: 636f29f16.
- `FauxResponseStep` scripted-message members (responseModel, providerThinkingLevel, thinkingLevel, diagnostics, rawStopReason, endTurn): f334aab28.

What is left, by cause (all are representation, placement, or lead-owned design):
- `Model::property:*` (10 rows): lg-rules renames to ProviderMeta, Capabilities and DisplayName.
- `DocumentObserver::property:watchDoc` and calls (7): Go methods cannot be generic; `WatchDocErased` plus the generic `WatchDoc` function.
- `InteractiveMode` `init`, `stop`, constructor, `showNewVersionNotification` (6): `Run` owns the lifecycle.
- `Theme` constructor, `colors`, `sourceInfo` (3): the Go `Theme` is a resolved ANSI struct.
- `ReadonlyFooterDataProvider` (reviewed gap): `SetFooter(factory any)` is untyped end to end; a typed footer factory is a reshape of `coding/extension/ui.go`, `ext_ui_context.go` and the subprocess bridge.
- `JsonAgentSessionEvent` (reviewed gap): Pi's TypeScript client casts parsed JSON to the union (rpc-client.ts:538), which `{Type, Raw}` mirrors; a typed decoder would be a Go-only addition.
- `UsageState` (reviewed gap): `JsonRepresentation<Usage>` is `ai.Usage`'s JSON form.
- `compact` (11 parameters), `UnauthorizedContext.fetch`, `AgentOptions.before/afterToolCall`, `toolChoice`, `Seg`, `GenerationInput`, `isEditToolResult`, `withFileMutationQueue`, `ModelRuntimeAuthOverrides`, `getAuth(model)`: rule requests as listed above.
- P1 not-exercised rows (telemetry `cardinality`/`sensitive`, `FauxProviderState.deferredFetchCount`, `ExecutionEnv` members): p1-impl.
