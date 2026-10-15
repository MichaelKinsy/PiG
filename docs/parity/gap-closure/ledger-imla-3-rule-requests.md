# lg-imla-3 (shard 3, top-down): findings and rule requests

State: integrate-042 9e25939e3 merged with lg-help-3 f44749ec0; `make interface-gaps` lists 166 of shard 3's 220 rows (165 after ledger-autobind-int a96afff9a, which closes none of the real gaps below). Every open group was checked against the Pi 1.0.4 source and the Go tree. Per-row labels: `labels/rule-requests-imla-3.tsv` (the 30 groups that `labels/rule-requests-help-3.tsv` does not list). lg-help-3 covered the rest in `ledger-help-3-rule-requests.md`.

## Result
No group in the top half needs a new Go member. Each open row is one of:
- **Placement** (a Go declaration exists, the detector's directory seeds miss it): `CodemodeWasmModule` is the module bytes: the `Wasm func() ([]byte, error)` option (`codemode/types.go`; nil is the embedded `codemode.QuickJSWasm()`); `copyJson` is `chord.CopyJSON` (its `omitUndefinedProperties` option has no counterpart because Go has no undefined); `Draft<T>` is type-level; `ServiceInstanceAddress` is `internal/chord/types.go`; `ListenerErrorHandler` is `internal/experimental/client/connection.go`; the durable, env, protocol and server groups are in lg-help-3's placement list (verified one by one: `git grep` finds `CommonDocDefinition`, `DocumentAddress`, `EntryDraft`, `SubmissionQuery`, `TaskOptions`, `RunningTask`, `CompactionHooks`, `HookRegistration`, `MessageChange`, `ToolTaskCheckpoint`, `DefineExtension`, `BinaryReader`, `JsonlStorageOptions`, `NodeSqliteDatabase`, `EnvConformanceCase`, `StorageConformanceProvider`, `SshError`, `ProtocolValidationError`, `RequestEnvelope`, `IsServerId`, `RoutedSessionHandle`).
- **Rename**: `ExtensionUIContext` (45 rows) is `coding/extension/ui.go#UIContext`, which has every upstream member (`TestUIContext_AllUpstreamMethodsPresent` in `coding/extension/ui_test.go`). The name rule picks `internal/codingagent/extensions.go#ExtensionUIContext` first. That is a six-method dialog subset that interactive mode uses (`TUIUIContext`, `NoopUI`). It is not a stale duplicate, so it stays. `renames.json` maps six members; the 22 open members need entries, and the rule must prefer the rename over the same-named older type. `OpenAICompletionsCompat` is `ai.OpenAICompat`. `ApiStreamSimpleFunction` is `ai.ModelsStreamFunction`. `getBuiltinImageModel` is `ai.GetImageModel`. `resolveCloudflareModel` is `ai.ResolveCloudflareBaseURL`. `MutableModels.getAuth(model)` is `Models.GetModelAuth`.
- **Error mechanics**: `name`, `stack` and `cause` of `PiMessagesResponseError`, `ServerError`, `SshError` and `ProtocolValidationError` come from `Error`. The owner approved this class for `ModelsError.name` (designed-out row `pkg:ai/models#ModelsError::property:name`, 2026-10-01).
- **Type-level or JavaScript-only**: `TranscriptMessages`, `JsonRepresentation`, `AgentToolCall` (an `Extract` alias), `bedrockProviderModule`, `setBedrockProviderModule`, per-API stream registries.
- **Type guard**: `isWriteToolResult`. Go type assertions replace guards on the sealed `ToolResultEvent`. Guards were ported, then removed as caller-free (aa9f1051b); do not re-add them.

## Rule requests for lg-rules
1. Placement seeds: `chord/.`, `client/.`, `codemode/.`, `durable/*`, `env/.`, `protocol/.`, `server/.` directories as listed above.
2. Rename entries: the `ExtensionUIContext` members and the five names above; make N4 win over an N1 match on a different, older type.
3. `T9`/`M3` rules: a single upstream function property is a Go `[]hook` field when the hooks compose in order (`AgentLoopConfig.beforeToolCall`, `afterToolCall`).
4. `S4`: an upstream `init` second parameter folds into `*http.Request` (`FetchFunction`).
5. `A3` object alias: a type alias of an object literal maps to the Go struct of the same name when every field matches (`PiMessagesRewriteImpact`).
6. Overload split: an upstream overload (`getAuth(model)`) maps to a Go method of another name that takes the overload's first parameter type (`GetModelAuth`).
7. Error mechanics: members `name`, `stack`, `cause` of a class that `extends Error` are satisfied by the Go `error` value.

## Evidence that no port was missed
`ResolveCloudflareBaseURL` substitutes the same placeholders as `resolveCloudflareModel`, but only for the two Cloudflare provider ids. Pi applies it in `cloudflareStreams`, which wraps only those providers, so the observable result is equal. `ResolveAzureBaseURL`, `GetModelAuth` (`ai/models_runtime_auth.go`) and `Models.GetAuth` cover both `getAuth` overloads. No commit in this lane changes Go code; the closing work is in the rules.

## Second pass (after LEAD-DECISION-P1 and ledger-port-needed.tsv)
Ported: `FauxContentBlock.TextSignature` (Pi `fauxText` returns `TextContent`, whose optional `textSignature` only the final message keeps). `TestFauxTextSignatureReachesOnlyTheFinalMessage` is mutation-checked. The detector's verdict for `fauxText::call:0` moved from "FauxContentBlock has no textSignature" to "has every member of TextContent, whose Go form is TextContent": the remaining request is a rule that accepts `FauxContentBlock` as the scripted form of `TextContent`.

Checked against Pi 1.0.4 and left as rule requests (a port would be a duplicate or a stand-in):
- `fauxAssistantMessage::call:0`: Pi's scripted step is an `AssistantMessage`, but the provider overwrites `api`, `provider` and `model` (`cloneMessage`) and `usage` (`withUsageEstimate`) on every resolved step (faux.ts:282, :230, :490). `FauxResponse` is the Go form of the step; fields for the overwritten members would never be read.
- `AgentLoopConfig.telemetryContext`, `.deferred`: `ai/telemetry_options_upstream_test.go` already ports `telemetry-options.test.ts` (eight provider and Models dispatches, two image dispatches). The P1 rule (b) must count a library-package field exercised by a Pi-oracle test.
- `createLsTool::call:0` (`AgentTool.constrainedSampling`): Pi's `ls` has no `constrainedSampling`; Go carries the member in `Schema()` (`ai.ToolSchema.ConstrainedSampling`), as for read, edit, write and shell tools.
- `EntryDraft.head`: `EntryId | "self"` is `Head *EntryId` plus `HeadSelf bool`, with the wire form in `durable/codecs.go`; a union-to-two-fields rule request.
- `getReadmePath`: Pig's docs bundle is the D22 path `prompts.docsSection` builds from `pigdocs.DocsDir`; a second helper would duplicate it.
- `SessionEntry` (T10d): Go keeps one raw-JSON `SessionEntry` and typed decoders per consumer (`AsMessage`, projected entries). Typed per-variant structs are a larger session-model change that needs a lead decision, not a rule.
