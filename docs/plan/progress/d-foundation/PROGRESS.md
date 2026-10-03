# d-foundation: pi-durable root package

Lane: d-foundation, from the agg-100 aggregate. Upstream `.upstream/v1.0.0/packages/durable`.

## Status

READY for review. The compiling API skeleton landed at `b9424b841`; other lanes merge the d-foundation branch. The root package, its JSON codecs, and the `chord/delta` Op/Draft switch (`016b0a100`, requested by d-session) are complete. Later requests from d-session (attached state, strict copyJson, exact checkpoint revisions, revokedDraft) landed through `2743b4971`. The owner decisions are under Blockers for review.

## Package layout

| Upstream | Go |
|---|---|
| `src/types.ts` | `durable/types.go` |
| `src/harness/types.ts`, the declarations `types.ts` references | `durable/harness_types.go` |
| `src/ids.ts` | `durable/ids.go` |
| `src/errors.ts` | `durable/errors.go` |
| `src/entries.ts` | `durable/entries.go` |
| `src/documents.ts` | `durable/documents.go` |
| `src/tasks.ts`, `AnyTask` of `harness/types.ts` | `durable/tasks.go` |
| `src/truncate.ts` | `durable/truncate.go` |
| `src/index.ts` | `durable/doc.go` (package doc) |
| `src/env/index.ts` (declarations only; no lane owns env) | `durable/env/env.go` |
| Chord `copyJson` for typed values | `durable/json.go` |

## Decisions other lanes depend on

- `context.Context` replaces Chord `Context` and comes first. A Promise is a blocking call that returns `(value, error)`.
- `JsonValue = any`, `JsonObject = map[string]any`. `Op = delta.Op` and `Draft[T] = *delta.Object` (T phantom) from d-session's `chord/delta` port (merged from `853a9f765`); `Tx.Doc` returns `*delta.Object`, `TxDoc[T]` returns it unchanged, and `DecodeDoc[T](draft.Snapshot())` reads it as T. `AttachedReplicatedState[T] = *chord.AttachedReplicatedState[T]` and `DocumentState[T] = *chord.AttachedReplicatedState[JsonObject]` (T phantom) from d-session's `chord/state.go` (merged from `38f16fbe7`); `durable` no longer imports pico3.
- IDs are distinct `int64` named types (`ConversationId`, `EntryId`, `TaskId`, `SubmissionId`, `DocumentId`) and `Seq`. `TaskId` does not carry the result type (Go methods cannot be generic). `Id` is the `~int64` constraint; `IdFromNumber[I]`, `SeqFromNumber`.
- `types.ts` and `harness/types.ts` import each other. Go forbids the cycle, so `durable` holds the part of `harness/types.ts` that `types.ts` reaches: `ModelRef`, `UserInput`, `SubmissionDraft`, `Submission`, `SettledTask`, `AnyTask`, `ConversationAbortOptions`, `ConversationHandle`, `ToolControl`, `ToolDiagnostic`, `ToolExecutionResult`, `ToolExecutionMode`, `QueueMode`, `ToolExecutionApi`, `ToolRegistration`, `PromptInput`, `PromptSection`, `HookRegistration`, `Wrap`, `Extension`, `RegistrySnapshot`, `Agent`, `ConversationStreamOptions`, `ConversationRetryPolicy`, `CompactionPolicy`, `CompactionReason`, `Settings`, `ContextView`. d-harness-a declares the rest of `harness/types.ts` (`AgentState`, `AgentChange`, `Conversation`, `Harness`, `HarnessOptions`, `HarnessSettings`, `HookApi`, the hook interfaces, `Registry`, `RegistryReader`, inspections) in `durable/harness` and may alias these.
- Overloaded upstream methods take erased tokens: `Tx.Doc(token AnyDocToken, args ...any) (any, error)`, `Tx.RetireDoc`, `Tx.CreateTaskErased(task AnyTask, input JsonValue, options)`, `DocumentReader.SnapshotErased`/`SnapshotAsOfErased`, `DocumentObserver.WatchDocErased`, `Session.DocumentStateErased`. `args` follow upstream order: owner ID (conversation/task scope), then family key, then seed (Tx.Doc on families). `ResolveAddress(definition, args)` parses them exactly as `resolveAddress`.
- Typed helpers: `TxDoc[T]`, `TxRetireDoc`, `Snapshot[T]`, `SnapshotAsOf[T]`, `TxEntry[D]`, `TxAppendEntry[D]`, `CreateTask[I,S,R,H]`, `DecodeTaskRecord`, `EncodeTaskRecord`, `DecodeDoc[T]`.
- Document tokens: `DocToken[T]` and `DocFamilyToken[T, I]`; the scope-refined names (`SessionDocToken`, ...) alias them. `AnyDocDefinition` is the erased definition the Session uses, over JSON objects. Definition-time TypeErrors (`DefineDoc` version, `DefineEntry` kind) panic with Pi's message.
- Task definitions: `TaskDefinition[I,S,R,H]` with `Phases map[string]PhaseHandler`, keyed by the checkpoint's JSON `phase` member. `Task.AnyDefinition()` returns `AnyTaskDefinition` with erased `Initial`, `Phases`, `Abort`, `Migrate`, `Hooks`; erased handlers take `ErasedRunningTask` and `ErasedTaskRuntime` and adapt `Commit` and `Hooks` to the typed task.
- `HookRunner[H].Each(name, invoke func(handlers H) error)`: Go has no keyed member access, so `invoke` receives the handler set and selects the member.
- Record unions that share fields (`TaskState`, `TaskOutcome`, `SubmissionRecord`, `DocumentRecord`, `DocumentContent`, `ContextEdit`) are one struct with a discriminant. `StorageWrite` and `CommitChange` are sealed interfaces: `ConversationWrite`, `EntryWrite`, `TaskWrite`, `SubmissionWrite`, `DocumentCreateWrite`, `DocumentCopyWrite`, `DocumentChangeWrite`, `DocumentRetireWrite`; `DocumentChange`, `DocumentCopyChange`.
- `Storage.entry` overloads become `Entry(ctx, id)` and `VisibleEntry(ctx, conversationId, id)`. `Storage.MintId()` returns `int64`.
- `ToJsonValue` is strict `copyJson`: `chord.CopyJSON` for JSON kinds, the JSON encoding for typed values, and a non-finite number fails with an error that `errors.Is(err, delta.ErrNotStrictJSON)` and whose message contains `strict JSON`. Erased document `Initial`/`Migrate` and task `Initial`/`Migrate` go through it. `FromJsonValue[T]` copies JSON-kind targets (`JsonValue`, `JsonObject`) with `chord.CopyJSON`, so family seeds are strict too. A `JsonObject` document's `CheckpointWhen` receives the exact prepared revision.
- JSON: `DecodeMessage`, `DecodeMessages`, `DecodeUserContent`; `EntryRecord`, `EntryDraft` (head number or `"self"`), `ContextEdit` and `TaskOutcome` (present null result) implement `MarshalJSON`/`UnmarshalJSON`. An optional `JsonValue` member (`Data`, `Detail`) treats nil as absent, so a stored JSON null decodes as absent.
- `SubmissionCreate` is its own struct without `Id`; `create.Record(id)`. `Commit[T](ctx, committer, change)` types a commit result.
- `env.ExecutionEnv`: `(T, error)` returns, the expected failure is `*env.FileError` or `*env.ExecutionError`.

## SQLite

The storage lane owns SQLite. `go.mod` already requires the pure-Go `modernc.org/sqlite v1.53.0` (CGO_ENABLED=0); use it. This lane adds no SQLite dependency.

## Red → green

The 45-minute skeleton deadline put the source before the tests, so no test was red-proven before its fix. Each test below was proven by a compiling mutation of the source that makes it fail; the source was then restored.

| Test | Upstream | Mutation that fails it |
|---|---|---|
| `durable#TestTruncateUtilitiesUpstream` (8 subtests) | `env-truncate.test.ts` :13 :23 :47 :54 :65 :75 :86 :95 | trailing-newline `bytes` branch → `lines` fails :65; first-line-exceeds branch disabled fails :86 |
| `durable#TestFormatSizeRoundsTiesLikeToFixed` | `truncate.ts` `formatSize` `toFixed(1)` (1280 B → `1.3KB`, Node-checked) | `%.1f` (ties to even) |
| `durable#TestTypesUpstream` (3 subtests) | `types.test.ts` :48 :72 :248 | type-checks Go snippets with `go/types` against `go list -export` data; the `widenedTask` positive control proves the checker accepts valid code |
| `durable#TestSpecUsageCompilesTheRootExamples` | `spec-usage.test.ts` :375 (root examples) | compile-only, as upstream |
| `durable#TestErasedPhasesRunTheTypedTask` | `tasks.ts` erasure that the scheduler drives | hook adapter that skips `invoke` |
| `durable#TestTaskOutcomeKeepsAPresentNullResult` | `TaskOutcome.result` of a `null`-result task | completed outcome without `result: null` |
| `durable#TestEntryDraftEncodesTheSelfHead` | `EntryDraft.head: EntryId \| "self"` | `"self"` head dropped |
| `durable#TestToJsonValueIsStrictCopyJson` | `chord/json.ts` `copyJson` on document initial values | no mutation run |
| `durable#TestDocumentConversionsKeepRevisionsAndStayStrict` | `session-checkpoints` `calls[0].value toBe snapshot`; `copyJson` on seeds | JSON round-trip instead of the exact revision; JSON encoding instead of `CopyJSON` for a JSON-kind target |
| `durable#TestEntryRecordRoundTripsMessagesByRole`, `TestDecodeUserContentAcceptsTextAndBlocks` | `EntryRecord.model`, `ContextEdit.messages`, `UserMessage.content` | no mutation run; round-trip assertions |

Commands: `go test ./durable/...`, `go vet ./durable/...`, `go tool golangci-lint run ./durable/...` (0 issues), `make port-map-drift`, `make interface-go-drift`, `make test-inventory`, `make check-scratch-paths`.

## Test mapping (`test-mapping-v1.0.0.json`)

- `env-truncate.test.ts`: designed-out → ported.
- `types.test.ts`: designed-out → partial. Missing: TaskId has no result type; the 15 union member-exclusivity `@ts-expect-error` literals and `missingPhase` have no compile-time Go form with discriminated structs; the `:248` harness checks (`defineExtension`, `hook`, `HooksOf`, `Harness`, `Conversation`) wait for d-harness-a.
- `spec-usage.test.ts`: designed-out → partial. Missing: the harness and tools examples; `revokedDraft` is ported over the plan-mode document because LiveDoc is a harness declaration.

## Blockers for review

- `make test-porting-release` now reports `types.test.ts` and `spec-usage.test.ts` as partial hot-path rows (in addition to the existing 2860 row), and `make known-gaps-drift` fails for the same reason. The harness parts close when d-harness-a lands. The Go-inexpressible type checks of `types.test.ts` stay open until the owner chooses one of two options: (a) redesign the record unions as sealed interfaces, or (b) approve a numbered reason for them.
- Optional `JsonValue` members (`EntryRecord.Data`, `SubmissionRecord.Detail`, `TaskOutcomeError.Detail`) treat nil as absent, so an explicit JSON `null` decodes as absent.
