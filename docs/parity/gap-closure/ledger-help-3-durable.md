# lg-help-3: package durable (open rows 895 -> rule requests and P1(b) tests)

Measured on integrate-042 plus `ledger-autobind-int` (library rule P1(b), S1c) with the placement seed `"durable": {"durable/..."}`.

## Closed by tests (Pi-cited, `mutation-checked:` marker)
Backends answer the Storage read contract like memory (`durable/storage/backend_reads_test.go`: Conversation, ScanConversations, Entry, FindLatestHeadMarker, ScanEntries, ScanTasks, Submission, ScanSubmissions, SubmissionByRequest, FindDocument, ScanDocuments, MintId on JsonlStorage, SqliteStorage and the `Storage` interface); tool input types and `EditToolDetails` wire names; `WatchTarget.Exclude`; `ShellExecOptions.Cwd`; `Shell.Cleanup`; `BinaryReader.Info`; `OpenDirReader`; `PreparedMemoryCommit.Seq`; the `SqliteDatabase` contract (rollback returns the callback error, idempotent Close).

## Rule requests (no Go change)
- **Generic method is a package function** (about 85 rows): `Session/TaskRuntime/ToolExecutionApi/HookApi.snapshot`, `snapshotAsOf`, `Session/TaskRuntime/ToolExecutionApi/DocumentObserver.watchDoc`, `Session.documentState`, `Tx/ToolExecutionApi.createTask` map to `durable.Snapshot[T]`, `SnapshotAsOf[T]`, `WatchDoc[T]`, `DocumentState[T]`, `CreateTask` (Go methods cannot have type parameters).
- **`Context` parameter is `context.Context`** (S4 "takes N, Go takes N-1" rows: `entry`, `prepareCommit`, `appendEntry`, `memo`, `output`, `Storage.*`, `MemoryStorage.*`): ledger-autobind-int S1c covers packages that import `Context` only from chord; durable is such a package.
- **Variadic key tail** (S3, `Tx.doc/retireDoc`): the document key and scope parameters are variadic `...any` selected by token kind.
- **Result type in the failure arm** (S13: `ok`, `err`, `toError`): Go returns `(T, error)`; `Result` helpers are kept for pairs.
- **Callback returns T** (S5: `transaction`): the Go callback returns `error`; the value flows through a closure.
- **`string | Uint8Array`** (T10: `writeFile`, `appendFile` content): Go takes `any` holding `string` or `[]byte`.
- **Intersection and alias types without a Go shape** (T12/A3/T9: `HookResult`, `HooksOf`, `TypedEntry`, `SubmissionCreate`, `DocumentCreate`, `LiveState`, `UsageState`, `JsonRepresentation`, `ToolExecutionResult.content`, `EntryRecord & { head }`): type-level only.
- **Discriminator typed as a closed union** (U5: `GenerationCheckpoint.phase`, `ToolTaskCheckpoint.phase`): the Go field is `string`.
- **Test-runner types with no Go counterpart** (`ExpectLike`, `StorageConformanceRunner`, `createExpectAssertions`, `EnvConformanceCase.timeoutMs`, `EnvConformanceOptions.symlinks`): Go uses `testing.TB` (`CreateTestingAssertions`) and `RegisterStorageConformance`.
- **Symbol-keyed members** (`DocToken[Symbol.docType]`, `DocFamilyToken[Symbol.docType]`): phantom type markers, no runtime member.
- **Error `cause`** (`ConversationBusy`, `ReadAfterWrite`): Pi sets no cause; DS5 applies.
- **`rangeDecoder`**: module-private helper of `env/index.ts`; Go decodes inside `ReadTextLines`.
