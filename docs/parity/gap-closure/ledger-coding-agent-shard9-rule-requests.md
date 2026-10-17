# Shard 9 (bottom-up) rule requests

Groups worked bottom-up from `ledger-resplit.tsv` on `ledger-autobind-int` (`go run ./test/parity/interface-closure/autobind -reach /tmp/reach-int.json -update`). Each group below is a detector rule or placement request, not a Go change: the Go symbol exists with the right behavior, or the only fix would add a caller-free member.

## Rule requests for lg-rules

- **S4, upstream explicit `context: Context` parameter against Go `ctx context.Context`** (`pkg:durable/.#Storage` 26 rows: `close`, `commit`, `conversation`, `entry` overloads, `scan*`, `submission*`, `task`, `document`, `findDocument`, `findLatestHeadMarker`; Go interface at `durable/types.go:963` already takes `ctx` first on all but `MintId`). The detector counts Pi's `context` parameter but not Go's `ctx`. `entry` has two overloads (`entry(id, context)` and `entry(conversationId, id, context)`): Go splits them into `Entry` and `VisibleEntry`.
- **Placement:** `pkg:tui/.#renderLatex` and `RenderLatexOptions` map to `internal/latex/latex.go#RenderLatex` and `#RenderLatexOptions`; `tui/markdown.go` calls it. Pi exports it from `tui`; Go keeps it in `internal/latex`.
- **JS `Error` members:** `name`, `stack`, `cause`, `message` on Error subclasses (`ServerDrainingError`, `McpHttpError.stack`, `StorageRejected.name/stack`) map to Go's `Error()`, `Unwrap()` and the concrete type name. `ServerDrainingError` has `Code`, `Message`, `Unwrap`.
- **Intersection with an object literal against an embedded struct:** `refreshAuthorization(options: TokenRequestOptions & { refreshToken })` is `oauth.RefreshAuthorizationOptions{TokenRequestOptions; RefreshToken}` (`mcp/oauth/flow.go:513`).
- **`McpFetch` function type against a one-method interface:** `UnauthorizedContext.fetch` is `(input, init?) => Promise<Response>`; Go's `McpFetch` is `Do(*http.Request)`, which `*http.Client` satisfies. Different calling convention by design (Go networking); needs a numbered divergence or an accepted-shape rule, not a signature change.
- **Intersection `Static<...>` protocol messages:** `AttachmentEnvelope` and `ServerMessage` are typebox `Static` types; Go has concrete structs in `internal/experimental/protocol/messages.go`.
- **`Uint8Array` against `[]byte`** (`StreamDecoder.decode`).
- **`SizeValue` (`number | "N%"`) against `tui.overlaySize` and `OverlayMargin` union** (5 `OverlayOptions` rows): a closed-union rule for Go structs with a numeric and a percentage form.

- **`readonly` member modifier in object literal types (detector bug):** `objectLiteral` (`autobind/rules.go`) compares the member name `readonly scope` with Go fields, so `defineDoc` (4 rows, `DocDefinition[T]`) and `defineDocFamily` (4 rows, `DocFamilyDefinition[T, I]`) report "no field for readonly scope/family"; strip a leading `readonly ` before the field lookup. `SnapshotEvent` ("no field for type") shows the same cause for a discriminator written `readonly type`.
- **Generic methods (`watchDoc<T>` overloads, `DocumentObserver`, 7 rows):** Go interfaces cannot carry type parameters on methods, so `durable.DocumentObserver` has `WatchDocErased(ctx, token, args...)` and the typed `WatchDoc` is a package function. Needs a documented rename `DocumentObserver.watchDoc => WatchDocErased` (all overloads).

## Left open on purpose (no Go change without a caller)

- `tui.CancellableLoader` and `tui.Loader` (11 rows): Go loaders are host-driven and take colour strings and frames; Pi's take `(ui, spinnerColorFn, messageColorFn, message, indicator)`, and have `start`, `stop`, `setText`, `setCustomBgFn`. Adding them means a timer and `TUI` ownership. The constructor-arity question was raised earlier and needs a design decision.
- `tui.SelectList` (`SelectedItem`, `OnCancel`, `OnSelect`, `OnSelectionChange`, `SetFilter`) and `EditorComponent` members: the Go components exist but no production path from `cmd/pig` reaches them (`FilterableList` is what production uses). The P1 rule wants a production caller; wiring or deleting is a product decision.
- `UnixServerOptions` members: `CreateUnixServer` has no caller from `cmd/pig`.
- `TelemetryAttributeMetadata.Cardinality`: no production read.
- `durable/env` `ExecutionEnv` (31 rows) and `durable/testing` storage benchmark rows: used only by `durable/durabletest`, which is test support in non-test files (see `ledger-dead-callers.tsv`).

## coding-agent groups (second pass, after merging lg-ca-rest-a at `61f3e3e0d`)

- **Placement renames:** `parseFrontmatter` is `internal/codingagent/frontmatter/frontmatter.go#Parse` (returns `Doc{Frontmatter, Body}`); `getAgentDir` is `internal/codingagent/paths.go#AgentDir` (N5 matched by shape to unrelated functions; needs the `get` prefix rule); `withFileMutationQueue` is `internal/codingagent/tools/mutation_queue.go#FileMutationQueue.With` (Go keeps the queue as a value with `With(filePath, fn)`).
- **Type-guard functions (`isBashToolResult`, `isEditToolResult`, `isFindToolResult`, `isGrepToolResult`, `isLsToolResult`, `isPowerShellToolResult`, `isReadToolResult`, `isWriteToolResult`, `isToolCallEventType` with its eight `call:<tool>` overloads):** TypeScript narrowing. Go's `extension.ToolResultEvent` and `ToolCallEvent` are sealed interfaces with one struct per tool, so a type switch or assertion is the equivalent. No production caller exists for an `IsXToolResult` wrapper, so adding one would be a caller-free member. Needs a designed-out rule for narrowing predicates, not a port.
- **`compact` (S4, 11 vs 8):** Go `compaction.Compact(ctx, prep, model, completer, streamFn, customInstructions, thinkingLevel, retryOptions, sessionID)` folds `apiKey`, `headers`, `env` into `SimpleCompleter`, `signal` into `ctx`, and `retry`+`callbacks` into `RetryOptions`. Needs an accepted-shape rule (credential bundle plus context), not more parameters.
- **`InteractiveMode`:** `init`, `stop`, `renderInitialMessages`, `getUserInput` (a `<-chan string` in Go) and the options `modelFallbackMessage` belong to the mode's lifecycle design (`Run` owns them). Not a rename.
- **`showNewVersionNotification(release: LatestPiRelease)`:** Go takes `BinaryUpdate`, a different shape (see the earlier undecidable record).

## Third pass (durable, chord, env)

- **`readonly` modifier again (U5):** `chord.WireServiceMemberSnapshot` reports "no field for the discriminator `readonly kind`"; strip `readonly ` in the discriminator parse as well as in `objectLiteral`.
- **`Record<string, never>` (T6):** `durable.GenerationInput` is `type GenerationInput struct{}`; an empty struct is the faithful form.
- **JSON-shaped aliases:** `UsageState` (`JsonRepresentation<...>` against `ai.Usage`), `JsonValue` (recursive union against `any`/`json.RawMessage`), `Seg` (`string | number`), `TaskState`, `TaskDocFamilyToken`, `RemotePlatform`, `TelemetryAttributeDefinition`: A3 alias bodies without a closed-union shape.
- **`chord/context#withCancel` (S4, 1 vs 0):** the same explicit-`Context` parameter as `durable.Storage`.
- **`ReplicatedStateSource.Attach` (3 rows):** only `AttachReplicatedState` calls it, and nothing from `cmd/pig` reaches `AttachReplicatedState`; wiring or deleting is a product decision.
- **Held, not changed:** `renderToolSample` R3 (malformed percent-encoding in a `$ref` segment) stays held as recorded in `gap-mcp-codemode-libs.md`.

## After merging lg-rules (`f02e43f6d`, gaps 6221, shard 9 live rows 183 in 79 groups)

lg-rules' `structuralAlias` and the A6 plain-object rule now run together (structural first). Still open from the requests above, with the new wording:

- **`readonly` modifier:** `TaskState` now reports "no field for the discriminator `readonly status`" and `WireServiceMemberSnapshot` "`readonly kind`" (U5); `defineDoc` reports "no field for `migrate?(value`", so the object-literal member splitter also mishandles a method-signature member (`migrate?(value: ...): ...`) and `readonly` members.
- **S4 `Context` parameter:** `durable.Storage` rows are now `not-exercised` (P1: production reaches `Storage` only from `durabletest`); the signature rows will appear again once reach changes.
- **`Static`, `Uint8Array`, `SizeValue`, `T & {...}`, `watchDoc`, `Error` members, `renderLatex` placement, `Record<string, never>`:** unchanged.

## After merging lg-rules a772550af (`60da6e929`, gaps 5508)

- **Closed by a Go change:** `chord.WireServiceMemberSnapshot` (U5 wanted a closed kind type; `ServiceMemberKind` added).
- **Detector suspect (`AgentOptions::property:initialState`, T12):** the row says `agent.AgentInitialState` "has no field for extensions". Pi's `AgentInitialState` is `Partial<Omit<AgentState, "pendingToolCalls" | "isStreaming" | "streamingMessage" | "errorMessage">>` and `AgentState` (agent/src/types.ts:382) declares no `extensions` member; the string appears nowhere in `packages/agent/src`. The member list is derived wrongly for `Partial<Omit<...>>` (probably the `Omit` key list); the Go struct has `SystemPrompt, Model, ThinkingLevel, Tools, Messages`, which is the complete set.
- **`SnapshotEvent` (T12, "no field for type"):** a single object-literal alias with a literal `type: "snapshot"` discriminator; Go carries the event type through its event interface, not a field. Needs a rule for a lone discriminator literal, or a documented rename.

## Truthful type equivalence for lg-rules (renames.json)

- `ModelRuntimeAuthOverrides` (coding-agent/src/core/model-runtime.ts:125) -> `AuthResolutionOverrides` (ai/auth_resolve.go:140). Pi's interface is `AuthOperationOptions` (only `signal`, which Go passes as the `ctx` argument) plus `apiKey`, `env`, `minOAuthValidityMs`. The Go struct carries exactly `APIKey`, `Env`, `MinOAuthValidityMs`. Clears 5 rows for the type and `ModelRuntime.getAuth` call:0's T9 parameter (`ModelRuntimeAuthOverrides` against `ai.AuthResolutionOverrides`). `typeRen` is keyed by the upstream short name, so the entry is one line.

## Stale `renames.json` keys: Go component types now carry Pi's names (for ledger-autobind)

18 coding-agent component member rows report `member-missing` ("no field or method for property render in tui/assistant_message_block.go#AssistantMessageComponent") although the Go method exists. `memberFor` consults `renames[prop.ID]` first, and each of these entries targets a Go type that has since been renamed to Pi's name (`AssistantMessageBlock` -> `AssistantMessageComponent`, `LoginDialog` -> `LoginDialogComponent`, `ModelSelector`, `OAuthSelector`, `UserMessageSelector` -> `...Component`, `UserMessageBlock`, `BashExecutionBlock`, `BranchSummaryComponent`). `renamedMember` finds no owner, returns nil, and the N1 rule never runs. Deleting the 19 keys (`/tmp/stalekeys.json` format: every `pkg:coding-agent/...::property:*` key whose target starts with `tui/` and names one of those old types) closes 18 rows (verified locally: re-derived with the 19 keys removed; `CompactionSummaryMessageComponent` stays open because its owner type has no Go symbol). Suggested guard: a sync test that fails when a renames target's owner type or member does not exist in pig-go.json (257 targets across mcp/codemode/coding-agent currently do not resolve by that check; the mcp/codemode ones may be a pig-go.json package-filter artefact and need a separate look).

Keys:
- `pkg:coding-agent/.#AssistantMessageComponent::property:render`
- `pkg:coding-agent/.#AssistantMessageComponent::property:setOutputPad`
- `pkg:coding-agent/.#BashExecutionComponent::property:appendOutput`
- `pkg:coding-agent/.#BashExecutionComponent::property:render`
- `pkg:coding-agent/.#BashExecutionComponent::property:setExpanded`
- `pkg:coding-agent/.#BranchSummaryMessageComponent::property:setExpanded`
- `pkg:coding-agent/.#CompactionSummaryMessageComponent::property:setExpanded`
- `pkg:coding-agent/.#LoginDialogComponent::property:render`
- `pkg:coding-agent/.#LoginDialogComponent::property:showAuth`
- `pkg:coding-agent/.#LoginDialogComponent::property:showDetails`
- `pkg:coding-agent/.#LoginDialogComponent::property:showInfo`
- `pkg:coding-agent/.#LoginDialogComponent::property:showProgress`
- `pkg:coding-agent/.#LoginDialogComponent::property:showWaiting`
- `pkg:coding-agent/.#ModelSelectorComponent::property:render`
- `pkg:coding-agent/.#OAuthSelectorComponent::property:render`
- `pkg:coding-agent/.#UserMessageComponent::property:invalidate`
- `pkg:coding-agent/.#UserMessageComponent::property:render`
- `pkg:coding-agent/.#UserMessageComponent::property:setOutputPad`
- `pkg:coding-agent/.#UserMessageSelectorComponent::property:render`

## After typing the closed unions (`9e61c2f57`, `e0513a611`)

- **`RpcCommand` (U5) moved from "bare string discriminator" to "the Go struct has no field for the property `message` of a union member".** `cmd/pig.RPCCommandEnvelope` carries `type` (now `RPCCommandType`, one constant per Pi literal) and the raw bytes; each command is its own Go struct (`RPCPromptCommand`, ...) that the dispatch switch unmarshals after reading the type. The Pi union is therefore realised as envelope + per-command structs, not one struct with every member. Request: accept a discriminated union whose members are Go structs reached through a typed discriminator switch (the same shape the detector already accepts for sealed-interface unions), or record `RpcCommand` as a reviewed equivalence to `RPCCommandEnvelope` plus the command structs.
- **`ProjectTrustDecision` (A8).** Pi `boolean | null` is `*bool` in Go (`type ProjectTrustDecision = *bool`, trust_manager.go:295): a nil pointer is `null`. A union of `boolean` and `null` should be a recognised shape for `*bool` (as `number | undefined` is `*int`).
- **`ResizedImage.data` (T1).** `internal/imageprocessing.ResizedImage.Data` is raw bytes where Pi holds base64 text; the type documents it. Needs either a reviewed equivalence (`string` base64 ~ `[]byte` for an internal image value) or a Go change to base64 across the image pipeline; not a closure I can prove from a test.
- **Four more U5 discriminators in other lanes' packages** (same fix as `ServiceProviderUpdateType`): `agent.ProxyAssistantMessageEvent.Type`, `ai.FauxContentBlock.Type`, `codemode.OutputItem.Type`, `mcp.ContentBlock.Type`.

## Component constructors (S3/S4, 21 rows) on integrate-042 + reviewed-gaps `{}` (`d7e899f0e` + merge)

`AgentSessionRuntime`, `AssistantMessageComponent`, `BashExecutionComponent`, `BorderedLoader`, `ExtensionEditorComponent`, `ExtensionRunner`, `ExtensionSelectorComponent`, `FooterComponent`, `InteractiveMode`, `KeybindingsManager`, `LoginDialogComponent`, `ModelSelectorComponent`, `SessionSelectorComponent`, `ToolExecutionComponent`, `TreeSelectorComponent`, `UserMessageComponent`, `UserMessageSelectorComponent` (+ `OAuthSelectorComponent`, `SkillInvocationMessageComponent`, `ModelRegistry`).

Cause: Pi components take `(tui, theme/keybindings, callbacks...)`; the Go components read the active theme and the TUI keybinding registry globally and report completion by polling (`Done()`, `Cancelled()`, `Selected...()`), driven by the editor-slot runners in `internal/codingagent/session_selectors.go`. `NewExtensionSelectorComponent(title, options, onToggleToolsExpanded...)` has ten production callers, every one a polling runner. Porting the callbacks (`onSelect`, `onCancel`) as lg-help-2 did for `OAuthSelectorComponent` means rewriting each runner around callbacks; the global theme/keybinding access cannot become constructor parameters without breaking the `Theme`/`ActiveTheme()` design. Decision needed: (a) accept these rows by a reviewed shape rule (constructor parameters that Pi uses for injected collaborators, `tui`, `theme`, `keybindings`, are satisfied by the Go package-level registries), plus the callback parameters as Go completion accessors, or (b) schedule the callback port per component with its runner (about 10 callers each for `ExtensionSelector`, `ModelSelector`, `SessionSelector`; the editor-slot flow also needs `SetFocus` wiring, which fixes the `focused` rows at the same time).
