# lg-imla-6: shard 6 (top-down) triage and rule requests

State: `staging/integrate-042` at 9e25939e3, with `make interface-gaps` run there (6082 gaps). Of the 220 rows in `ledger-resplit.tsv` with `shard == 6`, 150 are still gaps; the lanes merged before this run closed the other 70 (for example the `NEEDS-TEST` rows by 54b6beb1a and the `ViewportTUI` Container members).

I probed each remaining group against the pinned Pi 1.0.4 source and the Go tree. No remaining group is a behaviour that Go lacks and that production code could call. Each group is one of:

- (a) a Go spelling the detector cannot see (rename or placement),
- (b) a Go-shape fold that the lead answers already accept,
- (c) a Pi member whose Go port would be a caller-free member (the `prodreach` rule reopens those, and earlier lanes already removed such members in 0501acf95 and 162e747be),
- (d) a Pi `compat` export that Pi itself marks temporary.

Each group names the Go counterpart that I verified in the tree, so lg-rules can bind it without a new port. I wrote no port, on purpose: a port of any of these would duplicate an existing Go symbol or add a caller-free member.

36 of the 150 rows are `child-gap` rows and close when their owner closes.

## R1 Placement: the Go counterpart exists under another directory or name

| upstream row | Go counterpart (verified) |
|---|---|
| `chord/delta#encoder` | `internal/chord/delta/codec.go#NewEncoder` (`encoder()` returns an `Encoder`) |
| `chord/.#CopyJsonOptions` | `internal/chord/chordjson/chordjson.go`: `Copy` is `copyJson`, `Stored` is `copyJson` with `omitUndefinedProperties`; the options bag folds into two functions |
| `chord/.#ServiceProviderUpdate`, `chord/delta#Op` | `internal/chord/types.go#ServiceProviderUpdate`, `internal/chord/delta/delta.go#Op` |
| `protocol/.#ServerHello`, `protocol/.#FrameError` (4 rows), `client/.#DisconnectedError` (3 rows) | `internal/experimental/protocol/{messages,framing}.go`, `internal/experimental/client/transport.go` |
| `server/testing#*` (11 rows) | `internal/experimental/routing/routingtest/{client,host,server}.go` |
| `durable/.#Agent`, `CompactionPolicy`, `Settings`, `RegistrySnapshot.*` | `durable/harness_types.go`; `ConversationQuery`, `DocumentCopySource`, `EntryRecord`, `Id`, `Page`, `TaskOwnership`, `TypedEntry` are in `durable/types.go`; `SummaryRequest` is in `durable/harness/compaction.go`; `SqliteValue` is in `durable/storage/sqlite/database.go`; `ReadToolDetails` is in `durable/tools/read.go`; `createStorageConformance` is `durable/durabletest/storage_conformance.go#CreateStorageConformance` |
| `ai/api/google-shared#convertMessages` | `ai/google.go#geminiConvertMessages` (the detector picks `ai/openai.go#convertMessages`, which belongs to openai-completions) |
| `ai/providers/all#radiusProvider` | `ai/radius.go#NewRadiusProvider` (its comment says it mirrors `radiusProvider`) |
| `ai/api/azure-openai-responses#stream` | `ai/direct_simple.go#StreamSimple` dispatches by model API; Go has no per-API stream function |
| `mcp/.#isJsonRpcResponse` | `mcp/jsonrpc.go#JSONRPCMessage.IsResponse` (function to method). I probed 12 wire shapes (error as string, error without message, result plus error, null id, boolean id, and others) through `ParseJSONRPCMessage`: Go rejects and accepts the same shapes as Pi's `parseJsonRpcMessage`; `mcp/upstream_oracle_test.go#TestParseJSONRPCMessageMatchesPi` covers the oracle comparison |
| `coding-agent/.#ThemeSelectorComponent::construct:0` | `tui/theme_selector.go#NewThemeSelectorComponent` (f564c15bf renamed `NewThemeSelector`, so the name rule finds it) |
| `coding-agent/.#formatSkillsForPrompt` | `internal/codingagent/prompts/coding.go#formatSkills` (the detector sees five N5 candidates) |
| `coding-agent/.#main`, `main::call:0` | `coding/extension/host/subprocess/internal/noderuntimegen/main.go#main` is a `package main` command; the detector bound Pi's `main(argv, options)` to it by name. The Pi CLI entry is `cmd/pig`. Request: a `package main` `main` is not a candidate for a library function named `main` |
| `codemode/.#loadQuickJSWasm` | `codemode/assets.go` embeds `assets/quickjs.wasm`; Go has no lazy loader function |
| `telemetry/.#InMemoryTelemetryContext::construct:0` | `telemetry/memory.go#InMemoryTelemetryContext`: the zero value is the constructor |

## R2 Designed-out candidates: the Pi `compat` entrypoint

`packages/ai/src/compat.ts:1-12` describes itself as a "temporary compatibility entrypoint preserving the old global pi-ai API ... deleted with the coding-agent ModelManager migration". PiG has no global API registry. Request: record these rows as `designed-out` with that line as the reason:

- `ai/compat#ApiProvider`, `ai/compat#getApiProvider`
- `ai/compat#streamAzureOpenAIResponses`, `ai/compat#streamSimpleMistral`
- `ai/compat#RegisterFauxProviderOptions` (Go: `ai/faux.go#NewFauxProvider(FauxConfig)`)
- `ai/compat#lazyStream` (Go: `ai/provider_streams.go#LazyStream`)
- `ai/compat#FetchFunction` call rows (Go: `ai/types.go#FetchFunction` takes the `*http.Request`, which carries Pi's `(input, init)`)
- `ai/api/openai-codex-responses.lazy#openAICodexResponsesApi`, `ai/api/pi-messages.lazy#piMessagesApi`

## R3 Parameters that fold into a Go context, struct or closure

| rows | fold |
|---|---|
| `APIKeyAuth.check`, `APIKeyAuth.resolve` (`input.signal`) | the leading `context.Context` is the `AbortSignal`; `APIKeyAuthInput` has no `signal` field |
| `ApiKeyAuth.login` result `ApiKeyCredential` | `ai.Credential` with the API-key type |
| `ToolDefinition.execute` (5 upstream parameters), `RegisteredCommand.handler` (2 vs 1) | `ToolExecuteFunc` and the command handler take the extension context and update callback in one Go parameter or the leading context |
| `OverlayHandle.unfocus(options)` | `tui/tui.go#OverlayHandle.UnfocusWith(OverlayUnfocusOptions)` exists next to `Unfocus()` (tui.go:715-719) |
| `ViewportTUI.render/requestRender/stop` call rows | `render(width)` is `RenderSnapshot(width)`; `requestRender(force)` is `RequestRender()` plus `ForceFullRender()`; `stop()` is `Stop()` |
| `TuiAltScreenOptions.copySelection` (`boolean \| string`) | `func(string) error`: nil is `true`, an error's text is the message string |
| `telemetry/.#InMemoryTelemetryContext::startSpan::call:0` | a Go method cannot be generic: the result leaves through the closure; `StartSpan` returns the callback's error |
| `durable/.#watchEvents`, `FileWatcher.close`, `toError` | `WatchEvents(ctx, harness, id)`, `Close()` and `ToError(any) error` |
| `coding-agent/.#buildSessionContext` | `BuildSessionContext(entries, leafID...)`; Pi's `byId` is an optional prebuilt index, and no upstream caller passes it (sdk.ts:200, agent-session.ts:1797 call it without arguments) |
| `coding-agent/.#createCodingTools` | `CreateCodingTools` takes the settings view as a third parameter |
| `ai/.#Models::property:getAuth::call:1` | `string` is the model ID; `AnyModel` is a Go interface |

## R4 Type shapes

- Unions that Go spells as a sealed interface or marked struct: `ai/.#Message`, `ai/.#ClassifierQuestion`, `ai/compat#FauxContentBlock`, `ai/.#fauxThinking`, `ai/providers/faux#FauxResponseFactory`, `BashToolCallEvent.input`, `GrepToolInput`, `LsToolDetails.truncation`, `mcp/.#ImageContent`, `mcp/oauth#OAuthCallbackPage`, `durable/.#TaskOwnership` (A3, A4, T9, U5).
- `T | false | undefined` as a pointer plus a disabled flag: `Tool.constrainedSampling` and `ToolDefinition.constrainedSampling`.
- Ordered records as slices: `ClassifierChoiceAnswer.probabilities`, `BundleFacetPackageOptions.defaultFacets`.
- The type-level model map: `ModelTypeMap`, `AnyModel`, `keyof ModelTypeMap`, `getModelType`, `Models.getModelOfType`, `getModelsOfType`, `getAvailableOfType` and `ImageModel.type` are the Go `ModelType` string plus `AnyModel.ModelType()` (`ai/model_types.go:76,117`).
- `ResolvedGoogleThinkingLevel` is `ai/google.go#ResolvedGoogleThinkingLevel`, the named type of `Exclude<ModelThinkingLevel, "off">`.
- `agent/.#ThinkingLevel` is an alias of `ai.ModelThinkingLevel` (agent/types.go); request that A1 resolve its constants through the alias target (`ai.ThinkingOff` and the rest).
- `agent/.#agentLoop::call:0`: `agent.AgentEventStream` is the `EventStream`.
- Intersections that Go folds into `ai.StreamOptions` or `ai.DeferredFetchOptions` (lead answer 2): `ModelsDeferredFetchOptions`, `ModelsApiStreamOptions`, `ProviderImagesOptions`, `OpenAICompletionsOptions`. `ProviderModelConfig` and `ThemeBg` fail A4 (declared twice in the pinned sources).
- `tui/.#KeybindingDefinition.defaultKeys` (`KeyId | KeyId[]`) is `[]string`.
- `ResolveCliModelResult.model`: `Model` is `codingagent.RuntimeModel`.
- `ExtensionCommandContextActions` is `coding/extension/context_actions.go#CommandActions`.
- `ToolDefinition.outputSchema`, `prepareArguments`, `renderCall`: TypeBox `Static` and `TSchema` are `json.RawMessage`.

## R5 Language mechanics

- `McpAbortError.stack`, `McpAuthRequiredError.stack`, `DisconnectedError.name` and `stack`, `FrameError.name`, `cause` and `stack`: Go errors carry no stack. `McpAbortError` has `Name()` and `Cause()` (mcp/jsonrpc.go:331-345), as Pi's class has `name` and no constructor sets a cause (jsonrpc.ts:74-79).
- `tui/.#StackEntry`, `tui/.#StdinBufferEventMap`, `telemetry/.#InferStartAttributes`, `telemetry/.#TypedSpanStarter`: type-level constructs; `telemetry/schema.go#TypedSpanStarter` exists.

## R6 Pi members whose Go port would have no production caller

- `ViewportTUI.fullRedraws`, `onDebug`, `renderNow`, `terminal`, `wantsKeyRelease`, `[Symbol.VIEWPORT_TUI]`: 0501acf95 and 162e747be removed Go copies of these as caller-free.
- `ExtensionInputComponent.dispose`, `focused`, the five-parameter constructor and `handleInput`, and `ThemeSelectorComponent.getSelectList`: ported in f564c15bf (`NewExtensionInputComponent(title, placeholder, onSubmit, onCancel, ExtensionInputOptions)`, `Dispose`, `Focused`/`SetFocused`, `ThemeSelectorComponent.GetSelectList`). PiG's host still drives its dialogs through `ExtUIContext.runDialog` (`internal/codingagent/ext_ui_context.go:309`) with nil callbacks, so only the P1 library rule (members with direct tests and no `cmd/pig` caller) can close the rows that production does not reach; no host-driven placement rule or divergence is needed.

## Real gaps

None found in this half. No production code was changed.
