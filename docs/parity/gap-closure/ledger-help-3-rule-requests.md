# lg-help-3 (shard 3, bottom-up): ports and rule requests

State at the merge of integrate-042 (9babe4b66). The 49 groups from `pkg:coding-agent/.#ArminComponent` down to `pkg:tui/.#StackEntryOptions` of shard 3 were triaged against the upstream 1.0.4 source and the Go tree. Per-row labels: `labels/rule-requests-help-3.tsv` (group, id, family, Go symbol, detail).

## Ported here

- `tui.Loader.SetIndicator(*LoaderIndicatorOptions)` and `tui.LoaderIndicatorOptions{Frames, IntervalMs}`: upstream `setIndicator(indicator?)` (loader.ts): a nil indicator restores the default frames and the spinner color, a non-nil indicator renders verbatim, nil `Frames` keeps the default frames while an empty slice hides the indicator, and `IntervalMs` is used only when positive (a delay below one millisecond is one, as `setInterval` coerces it). The caller in `internal/codingagent/interactive_status.go` passes the working-indicator options. Test: `TestLoaderSetIndicatorOptions` (mutation: `IndicatorVerbatim` forced false fails five cases).
- `mcp.LLMContentType` with `LLMContentTypeText` and `LLMContentTypeImage`: upstream `LlmContent` is a closed `"text" | "image"` union; the field was a bare string. The conversion and `coding/mcpext` use the constants.

## Rule requests for lg-rules

### Placement (47 rows)
The seeds in `autobind/symbols.go` and `rules.Dirs` list no directory for these upstream packages, so every symbol reports `no-go-symbol` although the Go declaration exists:
- `env/.`: `SshError` is `env/ssh.go#SshError` (+`NewSshError`); `name`, `stack`, `cause` are Error mechanics.
- `durable/.`: `CommonDocDefinition`, `DocumentAddress`, `EntryDraft`, `SubmissionQuery`, `TaskOptions` (durable/types.go), `RunningTask` (generic alias of `TaskRecord`); `durable/harness`: `CompactionHooks`, `HookRegistration`, `MessageChange`, `ToolTaskCheckpoint`, `DefineExtension`; `durable/env`: `BinaryReader`; `durable/storage/jsonl`: `JsonlStorageOptions`; `durable/storage/sqlite/node`: `NodeSqliteDatabase`; `durable/durabletest` for upstream `durable/testing`: `EnvConformanceCase`, `StorageConformanceProvider`; `durable/tools`: `EditToolDetails` (the detector resolves it to the coding-agent type).
- `protocol/.`: `internal/experimental/protocol` has `ProtocolValidationError`, `RequestEnvelope`, `IsServerId(value string)`. `server/.`: `RoutedSessionHandle` is `internal/experimental/routing.RoutedSessionHandle`.
- `coding-agent/.#ExtensionRunner` is `coding/extension/host/inproc.Runner`.
- `coding-agent/.#getReadmePath` (with `getDocsPath`, `getExamplesPath`, shards 10 and 11): Pig's docs bundle (D22) is `internal/pigdocs.DocsDir(ConfigRoot())`, so `getReadmePath()` is `filepath.Join(pigdocs.DocsDir(ConfigRoot()), "README.md")` (same answer as lg-help-10's `getDocsPath` request); there is no examples directory (the prompt links the repository).
- `coding-agent/.#FileEntry`: the alias `SessionHeader | SessionEntry`.

### Rename (45 rows)
- `coding-agent/.#ExtensionUIContext` is `coding/extension/ui.go#UIContext`, which has every upstream member (`renames.json` already maps six). `internal/codingagent/extensions.go#ExtensionUIContext` is an older six-method dialog subset that the name rule picks first; the rule or a rename entry must prefer `UIContext`.
- `CreateAgentSessionRuntimeFactory::call:0`: upstream `projectTrustContext` is `CreateAgentSessionRuntimeOptions.ProjectTrustUI` (a `UIContext`, the only member of Pi's `ProjectTrustContext` a Go caller needs beyond `cwd`).
- `GrepToolDetails.truncation`: upstream `TruncationResult` is `extension.ToolTruncation` (also Find and Ls details).

### Constructors, options folding, parameters
- N3 should accept an unexported `new<Name>` when it is the type's only constructor (`ArminComponent`: `newArminComponent(random)`; `NewArminComponent` would be caller-free).
- Injected parameters fold into fewer Go parameters: `ExtensionEditorComponent` (tui, keybindings, callbacks, options, external editor command: the Go component is polled with `Done`, `Cancelled`, `Value`, `SetExternalEditor`), `Loader` (`NewStyledLoader(spinnerColor, messageColor, message, frames)`; the ui is the owner loop's invalidate), `createBashTool`/`createGrepTool`/`createFindTool`/`createPowerShellTool` (`binDir` stands in for the process-wide `getBinDir()`), `createEditTool` (`FileMutationQueue`).
- `createXToolDefinition` has no Go counterpart: Go tools are their own definition (`createBashToolDefinition`, `createLsToolDefinition`; same for the other tools).
- `ExtensionShortcut.handler`: the upstream `ctx: ExtensionContext` parameter is the Go context (`extension.FromContext`).
- `isJsonRpcNotification(message: unknown)`: Go takes the decoded `mcp.JSONRPCMessage`. `resolveModelScopeWithDiagnostics(modelRuntime)`: Go takes the consumer-owned `ModelScopeRuntime` subset. `createLsTool` result `AgentTool` is `agent.AgentTool`.

### Types
- `Focusable.focused` (interface property): the Go method is `SetFocused(bool)`; Go interfaces carry no fields.
- `StackEntryOptions.basis` (`number | "auto"`): `*int`, nil is `"auto"`.
- `UserBashEventResult` (either-or object union), `SessionEntry` (closed union of entry structs), `AudioContent` (`mcp.ContentBlock` is the one struct for every block type): A3 and N5 cases.

### Designed out
`defineTool` (identity helper for type inference), `ConversationDocToken` (TypeScript scope narrowing; Go has the runtime `Scope`), `InferEventAttributes` and `TelemetrySchemaSpanStartAttributes` (conditional types).

## Real gaps that need a decision or a larger port, not a rule
- `ReadToolCallEvent.input` and `WriteToolCallEvent.input` (and the other six tool inputs): PORTED in batch 3. `coding/extension` types `BashToolInput`, `ReadToolInput`, `EditToolInput`, `WriteToolInput`, `GrepToolInput`, `FindToolInput` and `LsToolInput` from the TypeBox schemas, with optional members as pointers and an `Extra` map that keeps undeclared members as upstream's plain objects do (`tool_input_json.go`). Tests: `TestToolCallEventInputsAreTypedAndKeepUndeclaredMembers`, `TestToolInputOmitsAbsentOptionalMembers`, the round-trip table in `marshalling_test.go` (mutation-checked). The conformance fixtures now build typed inputs.
- `CallToolResult` (3 rows): held for residual R1 of `gap-mcp-codemode-libs.md` (lenient block decoding).
- `ExtensionEditorComponent.handleInput`: contract `coding-agent/extension-editor/cancel-before-external-editor` (status pending, like the extension-input and extension-selector contracts) is in `behavior-contracts.toml` with `TestExtensionEditorHandleInputTransitions` (mutation-checked: swapping the cancel and external-editor tests fails the remapped-key case). B1 stays open because the detector reads `behaviorContracts` from a hand-written ledger row in `mapping-v1.0.4.json`, and no `handleInput` row has one yet; a ledger owner must author the row (`behaviorContracts`, layers, evidence) since `-update` resets pending rows. `focused` is not forwarded to the inner `Editor`: Go sets `Editor.Focused` once at construction because the editor-slot dialogs never take TUI focus.

## Cross-lane notes
- lg-help-10 R2 (a `Context` parameter is the leading `context.Context`) also covers `ExtensionShortcut.handler`, whose upstream `ctx: ExtensionContext` is read with `extension.FromContext`.
- `CallToolResult` R1 stays held: it needs lenient block decoding with JavaScript `undefined` and `TypeError` semantics for servers that violate the MCP schema; no caller or user path reaches it with a conforming server, so I did not port it in this batch.

## Batch 6: agent loop contexts carry tools (group `agent/.#PrepareNextTurnContext`)
`AgentTurnContext.Context`, `PrepareRequestContext.Context`, `AgentLoopTurnUpdate.Context` and `AgentRequestUpdate.Context` are now `agent.AgentContext{Messages, Tools}` (the update fields are `*AgentContext`, nil is upstream's `context ?? currentContext`). A returned context replaces the run's executable tools for later tool calls, as upstream `currentContext.tools` does; the session passes `s.agent.Tools()` (agent-session.ts `tools: this.agent.state.tools.slice()`). Tests: `TestAgentLoop_ContextUpdateReplacesExecutableTools` (mutation-checked) and the session MCP/codemode tool-loadout tests, which fail when the session returns no tools.
Rule requests for the other `AgentLoopConfig` rows: `beforeToolCall`/`afterToolCall` are `[]hook` in Go (composition of several hooks; the single-function property maps to a slice of hooks), `toolChoice` is `any` with the closed union decoded by the provider, `deferred` is `ai.DeferredOption` (`boolean | { window }`), and `AgentToolCall` is `agent.AgentToolCall` (an `Extract<>` alias).

## Batch 7: shard 3 ai groups 4-37 (left open by lg-ai-nogo-a)
Checked against 1.0.4 and the Go tree; none is a port that adds a caller. These rows belong to the families of `ledger-ai-shard6-rule-requests.md` and `ledger-ai-undecidable.md`:
- Mechanics and designed out: `AssistantMessageEventStream[Symbol.asyncIterator]` is `Events(ctx) iter.Seq`; `PiMessagesResponseError.cause` and `.stack` are Error mechanics; `TranscriptMessages` (`readonly { role: string }[]`) and `JsonRepresentation<T>` (conditional type) are type-level only.
- Placement and renames: `ProviderId`, `KnownProvider` (Go provider ids are plain strings on `Model.Provider`; a 42-constant set would have no caller), `OpenAICompletionsCompat`, `ModelsStoreOperationOptions`, `ApiStreamSimpleFunction`, `googleGenerativeAIApi`, `streamSimpleAzureOpenAIResponses`, `openAIResponsesApi`, `mistral-conversations#stream`, `bedrockProviderModule`/`setBedrockProviderModule` (Go builds providers from `BuiltinProviders()` by id; no stateless per-API stream registry).
- Parameter and result types: `MutableModels.getAuth(model: AnyModel)` and `resolveAzureBaseUrl(model)` take the provider string in Go; `getAvailableOfType`/`getModelOfType`/`getModelsOfType` return `ModelTypeMap[TType]` (generic indexed access; Go has typed methods per type); `fauxAssistantMessage(content: string | block | block[])` and `fauxText` use `[]FauxContentBlock`; `FetchFunction` folds the `init` options into the context-first Go function.
- Unions: `ClassifierAnswer`, `AssistantMessageFrame` (U4 discriminator read from MarshalJSON), `PiMessagesRewriteImpact`, `ModelsApiStreamOptions` (A3 intersection folded into `StreamOptions`). `ClassifierModel.type` is `ModelType()`.
- `StreamOptions.ToolChoice` is `any` on purpose: the per-provider options fold into `StreamOptions` (lead answer 2), and providers accept `auto`, `any`/`required`, `none` and a named tool, beyond Pi's `"auto" | "none"` `ToolChoice`.

## Batch 8: cross-shard observations (shard 11 rows seen while looking for unclaimed work; not ported here)
- Optional interface members: `mcp/.#AuthProvider::onUnauthorized?` is the separate Go interface `mcp.UnauthorizedHandler` (an optional capability the transport asserts), so a TypeScript optional method maps to an optional-capability interface.
- `tui/.#Keybinding` (`keyof Keybindings`), `KeybindingsManager.getKeys/getDefinition/matches(keybinding)`, `Keybindings` (51 properties) and `KeybindingsConfig`: Go keys are the strings of `tui.TUIKeybindings`; the 51 child rows name keys of that table.
- `tui/.#AutocompleteProvider::getSuggestions(options: { signal, force? })`: the signal is the leading `context.Context`, `force` the Go `bool`.
- `mcp/oauth#OAuthCallbackServer::waitForCallback` returns `oauth.CallbackWait` (OAuthCallback plus error); `tui/.#Tokens` is an external type (the ledger already says so).
