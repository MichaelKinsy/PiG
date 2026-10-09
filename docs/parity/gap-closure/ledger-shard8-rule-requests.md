# Shard 8 (bottom-up, lg-help-8): ported rows and rule requests

State: branch lg-help-8 on integrate-042 (9babe4b66) plus this lane's commits. lg-ca-member-b works the same shard from the other end; its commits (AzureEndpointOptions, PiMessagesOptions, ClampReasoning, tool-result guards, OAuthError, ExecutionError, JsonlCorruptionError, JsonlStorage, NodeExecutionEnv, PreparedMemoryCommit, SessionNotFoundError, IsReplace) are not repeated here.

## Ported in this lane (real missing members)
- `pkg:tui/.#Key`: `tui.Key` (`KeyHelper`) with every field and modifier builder of keys.ts:163, checked against Pi 1.0.4 `Key` probed with node (`tui/key_helper_test.go`); `ConfigSelectorComponent` uses `Key.Ctrl("c")`.
- `LoginDialogComponent::property:focused`: `SetFocused`/`Focused` on the dialog; the flag propagates to the prompt input (login-dialog.ts:21-29) and the host focuses the dialog while it is mounted (interactive-mode.ts:6306). `TestLoginDialogFocusPropagatesToPromptInput`; the Pi oracle test now focuses the dialog like its script does.
- `pkg:coding-agent/.#ThemeToken`: `TestThemeColorsAreKeyedByEveryThemeToken` (Theme.colors is a Record<ThemeToken, Color>).
- `SessionBeforeTreeResult::property:summary`: `SessionBeforeTreeResultSummary.Usage` is `*ai.Usage` (types.ts:1482), no longer `any`.
- `TestHarness` (server/testing): tests for `FailAttachmentRelease`, `NextServiceResult`, `GateNextClose` (`internal/experimental/routing/routingtest/harness_members_test.go`, mutation-checked on the gate).

- `pkg:coding-agent/.#ResizedImage`: the unexported `preparedImageResult` is now `imageprocessing.ResizedImage` with `MimeType`; `Data` stays `[]byte` where upstream carries base64 (rule: base64 string <-> `[]byte`). `resizeImage` and `formatDimensionNote` (other shards) take it as their result type: Go `prepareImageForLLM` / `formatDimensionNote`.
- `pkg:coding-agent/.#ToolExecutionOptions`: `tui.ToolExecutionOptions` (nil field is undefined) is the variadic last argument of `NewToolExecutionComponent`; all three construction sites pass the settings (interactive-mode.ts:3516, 3594, 3997). That fixed a bug: resumed tool cards ignored `terminal.showImages` and `imageWidthCells` (`TestResumedToolCardsUseTheImageSettings`, red before the fix). The remaining `ToolExecutionComponent` constructor rows (7 upstream parameters: toolCallId, args, definition, ui, cwd) are shard 1.

- `pkg:server/.#SessionNotFoundError::property:code`: `routing.ServerOperationErrorCode` (string-based, the five lifecycle codes as constants; chord codes convert) is now `ServerError.Code`'s type (errors.ts:3-8); the wire value is unchanged (`TestServerErrors...` literals, `requireServerCode`).

## Rule requests for lg-rules
### Stale rename entries
- `renames.json` lines 264-269 map `LoginDialogComponent::property:{render,showAuth,showDetails,showInfo,showProgress,showWaiting}` to owner `tui/login_dialog.go#LoginDialog`, a type that no longer exists. The owner is `LoginDialogComponent`; each member then resolves by name. `showPrompt` now matches `ShowPrompt` by name (1d7c4df20).
### Placement
- `server`, `server/testing`, `server/unix`, `protocol`, `env`, `client`: Go lives in `internal/experimental/{routing,routing/routingtest,protocol,env,client}`. Affected shard-8 groups: `Server`, `SessionNotFoundError`, `TestHarness`, `UnixListenerOptions`, `ServerId`, `Reply`, `ClientOptions`.
- `chord`, `chord/delta`, `chord/context`, `chord/bundler`: `internal/chord`, `chord/delta`, `internal/chord/chordctx`, `internal/experimental`. Groups: `Context`, `MutableReplicatedState`, `RemoteServiceEndpoint`, `WireServiceInstanceSnapshot`, `BundleFacetsOptions`, `createContextKey`.
- `durable/.`, `durable/env`, `durable/env/node`, `durable/storage/*`, `durable/testing`, `durable/tools`: the seeds do not list `durable/...`. Counterparts: `durable.DocumentId`, `durable/harness.AgentDoc`, `UsageDoc`, `CompactionResult`, `ToolHooks`, `GenerationCheckpoint`, `InboxItem`, `durable.ConversationRetryPolicy`, `PromptInput`, `SettledTask`, `TaskDefinition`, `TaskRecord`, `durable/env.ExecutionError`, `ShellExecOptions`, `WatchTarget`, `durable/env/node.NodeExecutionEnv`, `durable/storage/jsonl.JsonlStorage`, `durable/storage.PreparedMemoryCommit`, `durable/durabletest.StorageConformanceAssertions`, `EnvConformanceAssertions`, `RegisterStorageConformance` (for `registerStorageConformance`), `durable/tools.WriteToolInput`.
- `pkg:ai/api/*#stream` (bedrock-converse-stream, google-vertex): the Go counterpart is the provider's `Stream` method (`anthropicProvider.Stream` and the Google/Bedrock providers), not a package function.
- `pkg:ai/api/google-shared#getDisabledGoogleThinkingConfig`: `buildGeminiThinkingConfig` (ai/google.go:640) with `isReasoning` false; `GoogleApiThinkingLevel`: `GoogleThinkingLevel`.
- `pkg:ai/utils/provider-retry#retryProviderRequest`: `retryClassifierRequest` (ai/classifier_http.go:67), generic in the result type.
- `pkg:coding-agent/.#generateSummaryWithUsage` (and shard 7's `generateSummary`): ported as exported `compaction.GenerateSummaryWithUsage` (the former unexported `generateSummary`, same parameters), which `Compact` calls. The text-only `generateSummary` stays open: Pi exports it without an internal caller, and a Go wrapper in the internal compaction package would have no production caller. Remaining mismatch is the parameter list (upstream 13/14 positional: apiKey, headers, signal, env, callbacks fold into the completer and context): options-folding rule.
- `pkg:coding-agent/.#parseArgs` is `parseFlags` in `cmd/pig/args.go` (package main); `pkg:coding-agent/.#ExtensionHandler` is `extension.HandlerFn`; `LoadExtensionsResult` is the subprocess `Host.LoadAll` plus `Host.LoadErrors()` (no in-process extension runtime by design); `MainOptions` is the `cmd/pig` flag set.
- `pkg:coding-agent/.#createEditToolDefinition` -> `tools.CreateEditTool`, like lg-help-10's R6 for `createReadToolDefinition` and `createFindToolDefinition` (questions/lg-help-10.md:20): a Go built-in tool is its own definition (D73), so the family needs a rename rule, not a new adapter. Same for bash, write, grep, ls and powershell.
- `pkg:ai/providers/{ant-ling,meta,openrouter,xiaomi-token-plan-cn}#...Provider`: lg-ai-nogo-b ports these as `AntLingProvider`, `MetaProvider` ... (`ai/provider_constructors.go`); they need the `<id>Provider` -> `<Id>Provider` name rule only.
### Object-literal constants
- `pkg:tui/.#Key` (T12, rules.go:347): the upstream shape is `{ readonly escape: ...; ctrl: <K>(key) => ...; ... }`. The member name is read as `readonly escape`, so no field matches. Strip the `readonly ` modifier, and let a function-valued member match a method of the Go struct (`KeyHelper.Ctrl`).
### Names
- `pkg:tui/.#TUI_KEYBINDINGS` -> `tui.TUIKeybindings` (tui/keybindings.go:225), same for the upstream SCREAMING_SNAKE constants whose Go name keeps the `TUI` initialism.
- `pkg:tui/.#isViewportTUI` parameter `TUI` -> `tui.Renderer` (the Go renderer interface; `IsViewportTUI` is the lowered function).
### Inheritance and symbol-keyed members
- `pkg:tui/.#VStack::property:[Symbol.LAYOUT_NODE]` (also HStack): the method is `Stack.LayoutNode()` (tui/stack.go:147), promoted through the embedded `*Stack`; rule: `[Symbol.X]` property -> method `X`, and an inherited member is satisfied through an embedded field (LEAD-ANSWERS 1).
- `focused` on `LoginDialogComponent`, `ExtensionEditorComponent`, `ExtensionInputComponent`, `ModelSelectorComponent`, `OAuthSelectorComponent`, `SessionSelectorComponent`, `ThinkingSelectorComponent`, `TreeSelectorComponent`: the Go `Focusable` interface (`SetFocused`) with an optional `Focused()` getter stands for the `focused` accessor pair.
### Designed out (type level)
- `pkg:telemetry/.#SchemaTelemetrySpan`: type-level conditional type, already marked `stubgen:omit SchemaTelemetrySpan` in telemetry/schema.go:18; record it as designed-out with that marker.
- `chord/.#JsonRepresentation`, `chord/delta#PathRef`: TypeScript type-level aliases (`IsAny` conditional, `P | number`).
- `ai/compat#TSchema`: external TypeBox type.
- `BashToolInput`, `ReadToolInput` and the other `*ToolInput` (`Static<typeof schema>`): Go keeps them `any` on purpose (coding/extension/opaque_types.go:243): a `tool_call` handler edits the event's input in place, which a decoded struct value would not allow; the event's `WireInput` keeps member order.
### Unions, aliases and intersections
- `ModelsApiStreamOptions`, `ModelsSimpleStreamOptions`, `ModelsDeferredCancelOptions`: `T & ModelsRequestTransforms` -> the Go options struct embeds or holds the transform fields (A3 intersection rule).
- `OAuthClientInformationMixed`: `OAuthClientInformation | OAuthClientInformationFull`; Go `OAuthClientInformation` is the full superset (mcp/oauth/types.go).
- `ExchangeAuthorizationCode` options: `TokenRequestOptions & {...}` -> struct embedding `TokenRequestOptions` (mcp/oauth/flow.go:495).
- `ResolvedGoogleThinkingLevel` (`Exclude<ModelThinkingLevel, "off">`... named type of the remaining levels), `getModelType` (`keyof ModelTypeMap`), `ClassifierModel.type` (the `ModelType()` method), `FauxContentBlock.type`/`fauxAssistantMessage.content` (`string | block | block[]` -> `[]FauxContentBlock`), `AssistantMessageEvent` and `InputEventResult` (discriminated unions as sealed interfaces).
### Options, signatures
- `MistralOptions` (three declarations: ai, ai/compat, ai/api/mistral-conversations): its own members `toolChoice`, `promptMode`, `reasoningEffort` are `StreamOptions.ToolChoice`, `PromptMode`, `ReasoningEffort` (ai/types.go:696-700, wired in ai/mistral.go:76,307); the inherited members are the other `StreamOptions` fields. Rule: provider options type -> `StreamOptions` by json tag (LEAD-ANSWERS 2).
- `ModelsStoreEntry.models` (`readonly AnyModel[]`): Go keeps `[]json.RawMessage` on purpose (ai/models_store.go:19, each provider owns the catalog shape it persists).
- `AfterToolCallContext` -> `agent.ToolCallHookContext` plus the hook parameters (agent/tool_call_hook_context.go): Go passes the context through `context.Context`.
- `LoginDialogComponent` constructor (`providerId`, `onComplete`, optional names -> `providerName`, `onCancel`, variadic title), `showDeviceCode(info)` -> `ShowDeviceCode(uri, code)`, `showManualInput`/`showPrompt` returning `Promise<string>` -> `<-chan string`: options folding and Promise-to-channel rule. `handleInput` needs a state-transition entry in behavior-contracts.toml (B1); not written by this lane.
- `truncateToVisualLines(keep)`: upstream `keep` is a count; the string-union complaint is a rule false positive.
- `convertMessages` (openai-completions, 4 parameters): Go's `convertCompletionsMessages(messages, completionsConvertOptions)` folds model, compat and options.
- `AgentToolUpdateCallback` (function alias), `BeforeAgentStartEvent.systemPromptOptions` (`NormalizedBuildSystemPromptOptions` -> `BuildSystemPromptOptions`), `CompactionEntryDraft` (N5 several shapes), `getImageModel` (value plus found flag), `ModelsStoreEntry.models` (`json.RawMessage`).

### From lg-decide's port-needed list (shard 8), judged not to need a port
- `fauxAssistantMessage` result: Go returns the scripted `FauxResponse`, which the faux provider completes with `api`, `provider`, `model` and `usage` when it streams (ai/faux.go:61); a returned `AssistantMessage` would carry the same defaults twice. Representation rule `FauxResponse` <- `AssistantMessage` for scripted steps.
- `StorageConformanceAssertions.ok(value: unknown)`: Go `Ok(bool)`: the assertion is on truthiness already evaluated by the caller (TypeScript `unknown` truthiness has no Go form).
- `NodeExecutionEnv` constructor option `env: ProcessEnv` (`Record<string, string | undefined>`): Go `map[string]string`; an unset variable is an absent key.
- `AgentDoc`, `UsageDoc`: `RewindableConversationDocToken`/`ConversationDocToken` -> `durable.DocToken[T]` (generic instantiation rule P4).
- `parseArgs`: `cmd/pig/args.go` `parseFlags` (package main), result `Args` -> `cmd/pig.Args`.

## Shard 8 top-down (lg-imla-8): ported rows and rule requests
State: branch lg-imla-8 on lg-help-8 and lg-ca-member-b (merged), after `make interface-gaps-update`. Every remaining shard-8 gap was probed against the Go source; the groups below are the ones the earlier lanes did not record.

### Ported
- `pkg:chord/bundler#BundleFacetsOptions` members `minify`, `define`, `platform`, `target` (bundle.ts:27-30,94-115): `BundleFacetsOptions.Minify`, `Define`, `Platform` (`FacetBundlePlatform`) and `Target` reach esbuild through `bundleFacetEntry`, with Pi's defaults (platform node; target `node22.19` for node, `es2022` otherwise). `TestBundleFacetsAppliesMinifyDefinePlatformAndTarget` is mutation-checked on every option (minify, define, platform mapping, target, engines).

### Rule requests for lg-rules
- Leading `Context` parameter (chord `Context`, e.g. `MutableReplicatedState.change/replace(context, ...)`, `RemoteServiceEndpoint.invoke`): a parameter of upstream type `Context` is the leading `context.Context` (S1), as `AbortSignal` already is. Go's `chordctx` package ports `Context`, `createContextKey` (`chordctx.NewKey`), `withContextValue` (`WithValue`), `withAbortSignal`, `withoutAbortSignal`, `withCancel` and `awaitWithContext` (`Await`) onto `context.Context` (internal/chord/chordctx/chordctx.go). Groups: `chord/.#Context`, `chord/context#createContextKey`, `MutableReplicatedState`, `RemoteServiceEndpoint`.
- `pkg:chord/bundler#BundleFacetsOptions` `entries` (`Readonly<Record<string,string>>` -> ordered `[]FacetEntrySource`, which keeps `Object.entries` order) and `plugin` (`readonly id` -> `FacetBundlePlugin.Id`; strip `readonly` as for `tui.Key`).
- `pkg:chord/delta#isReplace(op: Op | WireOp)`: Go `IsReplace(delta.Op)`; `WireOp` is the encoded form and has its own Go type; union-of-two-shapes -> the decoded type.
- Type guards `isBashToolResult`, `isReadToolResult`, ... (`coding-agent/.#is*ToolResult`, `isToolCallEventType`): compile-time narrowing of one runtime shape (`toolName`); Go's `CustomToolResultEvent` keeps `ToolName` as a field (docs/parity/gap-closure/gap-ledger-followups.md Gap 12). Designed out at the type level; a Go `IsBashToolResult(e) bool` would be one comparison with no caller.
- `Server.start()` returning `this`: Go `Server.Start(ctx) error`; chaining has no Go form (rule: a method returning its receiver maps to no return value).
- `TestHarness.terminated` (`Promise<Error | undefined>`): `Terminated() <-chan struct{}` plus `TerminalError()`; Promise of a value or failure -> signal channel plus accessor.
- `SessionNotFoundError`, `OAuthError`, `ExecutionError`, `JsonlCorruptionError` members `name`, `stack`: a Go error has no `name` or `stack`; `cause` is `Unwrap()`. Designed out (`Error.name`/`Error.stack` are JavaScript runtime members; `Error()` carries the message).
- Placement for `durable/...` groups (`NodeExecutionEnv`, `JsonlStorage`, `StorageConformanceAssertions`, `ExecutionError`, `Reply`, ...): their members are all present with the leading `context.Context` (Go `Cleanup()`/`Close()` take no context where Pi passes an unused `Context`); the `S4` complaints on those groups are the same leading-Context rule as above.
