# Shard 6 (bottom-up): what is a rule request and what is a real gap

State at 43762d0b3, after merging integrate-042 and lg-ai-types-b. 190 shard-6 rows were live before the merge, about 140 after (lg-ai-types-b closed the `ViewportTUI` Container members, `SettingsListTheme`, tool options). For each remaining group, a port would add a caller-free Go member or a Go-only type, which the lane rules forbid, or the Go shape is already faithful and the detector cannot see it. Requests for lg-rules, by rule family:

## Placement
- `protocol`, `client`, `chord`, `server/testing`, `durable` packages: the Go code lives under `internal/experimental/*` and `durable/*` with different top-level names. `FrameError`, `DisconnectedError`, `ServerHello`, `ProtocolTestClient`, `TestServerHost`, `ByteTransportHandlers`, `CopyJsonOptions`, `Op`, `encoder`, `ServiceProviderUpdate`, `Agent`, `EntryRecord`, `Settings` and the other `durable/.#` names should resolve through a per-package placement entry.
- `pkg:ai/api/google-shared#convertMessages` resolves to `ai/openai.go#convertMessages`; the Go counterpart is `ai/google.go#geminiConvertMessages` (placement entry for google-shared).
- `telemetry/.#InMemoryTelemetryContext` exists in `telemetry/memory.go`; the placement of `pkg:telemetry/.` misses it.

## Naming and constructors
- `getApiProvider`, `ApiProvider`, `streamAzureOpenAIResponses`, `streamSimpleMistral`, `openAICodexResponsesApi`, `piMessagesApi`, `xiaomiProvider`, `huggingfaceProvider`, `opencodeGoProvider`, `radiusProvider`: Go has no stateless per-API stream registry. Streams go through a provider built from `BuiltinProviders()` by id and `ProviderStreams`. These are design differences, not renames; each needs a recorded `designed-out` or a named mapping to `BuiltinProviders`.
- `NewThemeSelectorComponent` (3 rows): the Go constructor takes `(currentTheme, callbacks)` as a struct; request a rule for a constructor with a differently named Go function.

## Types
- `agent.ThinkingLevel` (A1): Go declares it as an alias of `ai.ModelThinkingLevel`, whose constants are in package ai. Request: A1 resolves constants through an alias target.
- `ImageModel.type` and `ClassifierModel.type`: the discriminator is the `ModelType()` method.
- `ClassifierChoiceAnswer.probabilities`, `ClassifierChoiceQuestion.criteria`: upstream records are ordered slices in Go (U/T6 rule for ordered records).
- `ResolvedGoogleThinkingLevel`: `Exclude<ModelThinkingLevel, "off">`, a Go named type of the remaining levels.
- `GoogleOptions`, `GoogleVertexOptions`, `OpenAICompletionsOptions` and the other per-API options types: Go folds them into `StreamOptions` (fields carry the upstream names, for example `Project`, `Location`, `BearerToken`). A Go-only option type per API would have no caller, so these need a rule "options type maps to `StreamOptions` field subset".
- `Tool.constrainedSampling` and `ToolDefinition.constrainedSampling` (`false | config`): Go uses a pointer plus `ConstrainedSamplingDisabled`.
- A3 intersections and unions (`ModelsDeferredFetchOptions`, `ModelsApiStreamOptions`, `ProviderImagesOptions`, `Message`, `AnyModel`, `ClassifierQuestion`): see `ledger-ai-undecidable.md`.

## Signatures
- `APIKeyAuthInput.signal` (3 rows), `FetchFunction` (2 rows), `OverlayHandle.unfocus` (the options bag), `RegisteredCommand.handler`, constructors with 5 to 9 upstream parameters (`ExtensionInputComponent`, `ModelSelectorComponent`, `SessionSelectorComponent`): context, options struct and injected dependencies fold into fewer Go parameters.
- `buildSessionContext(byId)`: the Go parameter is the session store, not a record of entries.

## Language mechanics
- `McpAbortError.stack`, `McpAuthRequiredError.stack`: Go errors carry no stack.
- `StackEntry`, `StdinBufferEventMap`, `TypedSpanStarter`, `InferStartAttributes`, `SqliteValue`, `Page`, `Id`: TypeScript-only type-level constructs.

## Real gaps
None among the remaining shard-6 rows that can be ported without adding a caller-free member. The ai rows I could port earlier are already merged (see `ledger-ai.md` and `ledger-ai-undecidable.md`).
