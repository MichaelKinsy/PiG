# gap-behavior-core: package durable, open interface rows by root-cause family

Measured on integrate-042 merged with ledger-autobind-int (`make interface-gaps`, package durable): 981 open rows at the start, 945 after this lane's first batch (36 type and function rows exercised by asserting, mutation-checked tests). The detector's accounting of the 945:

| reason | rows | owner |
|---|---:|---|
| not-exercised | 620 | P1 library reachability (rule request D3 in `lg-help-1-durable-rule-requests.md`) |
| rule families below | 244 | the rules engine: each row's Go form already exists |
| child-gap | 81 | closes with its owner row |

No row in the rule families needs a Go source change. Each Go shape was checked in `durable/` and is the idiomatic form of the upstream member. `make interface-gaps` lists the rows by id (`build/interface-gaps/gaps.tsv`).

## Rule requests (one rule closes a whole family)

| family | rows | upstream | Go form (exists) | rule |
|---|---:|---|---|---|
| D2 generic methods | 79 | `Session`, `TaskRuntime`, `ToolExecutionApi`, `HookApi`, `DocumentObserver`, `Tx` members `snapshot`, `snapshotAsOf`, `watchDoc`, `documentState`, `createTask` and their `::call:N` rows | package functions `durable.Snapshot[T]`, `SnapshotAsOf[T]`, `WatchDoc[T]`, `CreateTask[I,S,R,H]` (`durable/documents.go:301-354`, `tasks.go:54`); Go methods cannot take type parameters | an upstream member whose signature has a type parameter is satisfied by a package-level function of the same name whose first non-context parameter is the owner or its reader or observer interface; the call rows follow the owner |
| D2b per-token overloads | 12 | `Tx.doc`, `Tx.retireDoc` overloads that differ in the trailing identity parameters (`taskId`, `conversationId`, `key`) | `Tx.Doc(token, args ...any)`, `Tx.RetireDoc(token, args ...any)` (`types.go:854`) | S3 accepts a required upstream parameter in a Go variadic tail when the overload set differs only in the trailing identity parameters of a token-typed first parameter |
| R Result | 70 | `FileSystem`, `ExecutionEnv`, `NodeExecutionEnv`, `BinaryReader`, `TextLineReader`, `DirReader`, `Shell` methods returning `Result<T, FileError>` and `Result<void, FileError>` | `(T, error)` and `error`, the error being `*env.FileError` (`durable/env/env.go`); `env.Result[T,E]`, `Ok`, `Err`, `GetOrThrow` cover the rare value form | S5/T9/T10d: `Result<T, E>` is `(T, error)` and `Result<void, E>` is `error` for a call result; the union members `ok`, `value`, `error` are not struct fields |
| O Context-last overloads | 15 | `Storage.entry`, `memo`, `ToolExecutionApi.output`, `MemoryStorage.prepareCommit`, `Tx.appendEntry` second overloads | separate Go methods (`Entry` and `VisibleEntry`, `types.go:974-976`) or a variadic tail (`Memo(ctx, name, candidate ...)`) | an upstream overload set maps to one Go method plus named siblings or a variadic tail; the upstream `Context` is the leading `ctx` (S1c) |
| U intersection results | 7 | `findLatestHeadMarker` returns `EntryRecord & { readonly head: EntryId }` | `*EntryRecord` with `Head` set (`types.go:979`) | T12: an intersection of a record type with a required-property literal is that record type |
| D4 Error fields | 14 | `name`, `stack`, `cause` on `ConversationBusy`, `ExecutionError`, `FileError`, `JsonlCorruptionError`, `ReadAfterWrite`, `StorageRejected` | `Error()`, `Unwrap()` (`cause` is `Unwrap`) | designed out: JavaScript `Error` members have no Go reader |
| D4 brand | 2 | `[Symbol.docType]` on `DocToken`, `DocFamilyToken` | generic `DocToken[T]` | designed out: TypeScript phantom brand |
| N type-level helpers | 8 | `HooksOf`, `LatestConversationSemantics`, `RewindableConversationSemantics`, `rangeDecoder`, `ExpectLike`, `StorageConformanceRunner`, `createExpectAssertions`, the `JsonlCorruptionError` constructor | generic hook types, `durabletest.CreateTestingAssertions`, `RegisterStorageConformance`, a struct literal of the error | designed out: TypeScript typing aids and a Vitest adapter; Go's `testing.TB` replaces them |
| X shape reviews | 37 | unions and mapped types (`HarnessOptions`, `TaskDefinition`, `ToolRegistration`, `EntryDraft`, `TypedEntry`, `DocumentCreate`, `SubmissionCreate`, `DocFamilyDefinition`), the sqlite `transaction` callback, `registerEnvConformance`, `timeoutMs`, `symlinks` | generic structs, sealed interfaces, `Timeout time.Duration`, `NoSymlinks` | per-row review in `gap-durable.md` ("Go shape"); each needs its own rule or a numbered divergence |

## Done in this lane

Packages `durable`, `durable/env`, `durable/env/node`, `durable/harness`, `durable/storage` and `durable/tools` gained tests for the typed tool inputs and `EditToolDetails`; the `ToolTaskInput`, `ToolTaskResult`, `GenerationResult`, `SummaryRequest` and `CompactionCheckpoint` wire shapes; the built-in retry and compaction policies; `DefineTool` and `DefineExtension`; `ConversationInit`; `TableCommitChange`, `DocumentCommitChange`, `DocumentCopySource` and `DocumentState`; `NewFileError`, `NewExecutionError` and `GetOrUndefined`; and the conformance harness types (`StorageConformanceAssertions`, `StorageConformanceProvider`, `RegisterStorageConformance`, `EnvConformance*`, `CreateEnvConformance`). 21 mutants were applied through `go test -overlay` and in place; all were killed.
