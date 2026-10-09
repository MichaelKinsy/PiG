# Library packages: the 38 undecidable ledger rows, by cause, with the Go form and the proposed decision

Rows are from the detector on the p1-impl engine (durable 30, chord 4, telemetry 4, server 1, mcp 1). "Undecidable" means the engine met a TypeScript type it has no rule for. Each class below names the Go form that exists today and the decision the engine owner can record. No row needs a Go change.

## U1. Intersection with a union, `Omit`, `Extract` or a conditional operand (A7, 20 rows)

`ContextEdit`, `DocDefinition`, `DocFamilyDefinition`, `DocumentRecord`, `EntryDraft`, `RunningTask`, `SettledSubmissionRecord`, `SettledTask`, `SubmissionDraft`, `TaskRecord`, `ToolRegistration`, `TypedEntry`, `TypedEntryDraft` (durable) and `TelemetryAttributeDefinition`, `TelemetryEventAttributeDefinition`, `TelemetryStartAttributeDefinition` (telemetry).

Pi composes these with `&` over a discriminated union (`TaskRecord = Base & ({ state: ... } | { state: ... })`), `Omit<EntryRecord, "id" | ...>` or `Extract<TaskState<S, R>, ...>`. Go has one struct per record holding the union's discriminator and every member's fields (`durable.TaskRecord`, `durable.EntryRecord`, `telemetry.TelemetryAttributeDefinition` with embedded `TelemetryAttributeMetadata` and `Type`, `Values`, `ElementValues`, `Examples`). `Omit` drafts are the Go draft structs without the omitted fields. Decision: an intersection whose operands are a union or a utility type resolves operand by operand: each Pi member must exist as a Go field of the single struct (the engine already checks fields), and the union members map to the discriminator's Go constants (rule R5 of `ledger-library-rule-requests.md`).

## U2. `HookResult<T>` as a result or parameter (T9, 6 rows)

`CompactionHooks.beforeCompact`, `GenerationHooks.beforeRequest`, `GenerationHooks.onYield`, `ToolHooks.afterTool`, `ToolHooks.beforeTool` return `HookResult<T> = T | undefined | Promise<T | undefined>` (harness/types.ts:612). Go returns `(*T, error)`: nil is `undefined`, the call blocks where Pi awaits, and an error is a rejection (the async-contract rule in AGENTS.md). Decision: `HookResult<T>` maps to `(*T, error)`; the engine compares `T` with the Go pointer's element type (`CompactionDecision`, `GenerationRequest`, `YieldContinue`, `ToolExecutionResult`, `BeforeToolResult`).

## U3. Generic tokens and tasks erased to an interface (T9, 4 rows)

`HookApi.snapshot`, `ToolExecutionApi.snapshot` and `watchDoc` take `SessionDocToken<...>`; `ToolExecutionApi.createTask` takes `Task<...>`; Go takes `durable.AnyDocToken` and `durable.AnyTask`, the interfaces every generic token or task implements (durable/documents.go:32, durable/tasks.go:36). Decision: a generic class parameter maps to its Go `Any*` interface.

## U4. `EntryRecord & { head: EntryId }` result (T12, 6 rows)

`findLatestHeadMarker` of `Storage`, `MemoryStorage`, `JsonlStorage`, `SqliteStorage` returns an `EntryRecord` known to carry `head`. Go returns `(*EntryRecord, error)` and `EntryRecord.Head` is a pointer that is non-nil for a head marker (durable/types.go:977-979). Decision: an intersection that only narrows an optional field to present maps to the base struct; the guarantee is the method's documented contract, asserted by the storage read tests (`durable/storage/storage_read_contract_test.go`, `memory_storage_reads_test.go`, `sqlite/sqlite_storage_test.go`).

## U5. Mapped types over a union, `TaskDefinition` (T12, 3 rows)

`TaskDefinition.phases` is `{ [P in S["phase"]]: PhaseHandler<...> }`; Go uses `map[string]PhaseHandler[I, S, R, H]` and the task registry checks each phase name at definition time. `Change.state` (chord, `{ -readonly [Key in keyof T]: ... }`) maps to `delta.Object`; `defineService` options (`readonly [options: never]`, chord) is the TypeScript spelling of "no options argument" Decision: a mapped type keyed by a string-literal union maps to a Go `map[string]` whose keys the constructor validates; `defineService`'s `readonly [options: never]` rest parameter is the TypeScript spelling of "no options for this schema"; Go's `DefineService[T](id, options ...ServiceOptions)` (internal/chord/service.go:70) takes the options as a variadic tail, so the row needs the optional-parameter rule (R7).

## U6. Union with `undefined` or a promise (T10, 3 rows)

`BundleFacetsOptions.target` (`string | readonly string[] | undefined`) is Go `[]string` (nil is `undefined`, one string is a one-element slice); `CodemodeSandboxOptions.wasm` (`object | Promise<object> | undefined`) is `func() ([]byte, error)`, the Go form of a possibly-asynchronous provider; `HarnessOptions` takes `HarnessSettings` or a provider of it, Go `func() *harness.HarnessSettings`. Decision: `T | Promise<T> | undefined` maps to a provider function returning `(T, error)`; `string | string[]` maps to `[]string`.

## U7. Remaining

- `ReplicatedState` (chord, N5): Pi's one `ReplicatedState` class matches `AttachedReplicatedState`, `MutableReplicatedState`, `MutableReplicatedStateOf` in `internal/chord`; Pi's class is the mutable and attached forms together. Decision: map to `MutableReplicatedState`, with `AttachedReplicatedState` its attached view.
- `SupportedProtocolVersion` (mcp, A3): `(typeof SUPPORTED_PROTOCOL_VERSIONS)[number]` is the string type of the four listed versions; Go declares `SupportedProtocolVersion` as a string type with `SupportedProtocolVersions` the list (mcp/types.go:13-16). Decision: an indexed-access alias over an `as const` array maps to the Go string type plus the exported list.
- `TypedSpanStarter` (telemetry, T9): `UnionToIntersection` computes the combined span vocabulary of the schema tuple at the type level; Go's `CreateTypedSpanStarter` takes one or more schemas and returns one function (telemetry/schema.go). Decision: designed out as a type-level helper (R3).
- `TestHarness.attachClient` (server, T12b): Pi returns the object `{ invokeService, release(context) }` (server/src/testing/host.ts:45-48). The engine finds no `release` method on the Go interface it paired the result with; the Go routed attachment type carries `Release(ctx) error` (internal/experimental/routing). The server owner confirms which Go type the rule pairs.
