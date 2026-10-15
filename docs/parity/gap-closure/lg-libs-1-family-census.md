# lg-libs-1: interface-ledger census of the library packages (chord, codemode, mcp, telemetry, evals, protocol, client, server, env, durable)

Source: `go run ./test/parity/interface-closure/autobind` on integrate-042 (0ac206dba) with the P1 detector. 4,198 gap rows remain in total; the library packages hold about 1,350 of them. Pi 1.0.4.

## Renames added (renames.json)

Session, TaskRuntime and DocumentObserver carry the document reads as methods taking a typed token (`snapshot`, `snapshotAsOf`, `watchDoc`, `documentState`). A Go method has no type parameters, so the interface exposes the erased forms and the generic package functions wrap them. The nine entries map each Pi property to the erased Go member, as the existing `HookApi` entries do (`DocumentReader.SnapshotErased`, `SnapshotAsOfErased`, `DocumentObserver.WatchDocErased`, `Session.DocumentStateErased`, `Tx.CreateTaskErased`). Result with the detector: each property now resolves to its Go member (reason `member-missing` becomes `child-gap`); the overload call rows below it are still gaps for rule S3. No Go code changed.

## What blocks the remaining rows (rule or owner decisions, not code)

| family | rows | cause | owner |
|---|---:|---|---|
| S4 parameter count: Pi's trailing `context: Context` against Go's leading `context.Context` | durable 245, env 27, server 16, chord 3, client 3, mcp 1 | rule S12 (Context parameter) exists on lg-imla-4 and gap-behavior-durable-env, not on integrate-042; every `Conversation.*`, `Session.*`, `Tx.*` member with a Go `ctx` first parameter shows "upstream takes N, Go takes N-1" | rules lane |
| S3 overloads into one erased variadic: `snapshot`/`snapshotAsOf`/`watchDoc` have six Pi overloads (singleton, conversation, task, keyed, ...) and the Go erased member takes `args ...any` | durable 72 | rule request: an overload whose differing parameters are the document's keying arguments maps to a Go variadic tail | rules lane |
| T9 JSON representation: Pi `JsonValue`, `CodemodeJsonSchema` against Go `json.RawMessage` | codemode 14, chord 7 | `representations.json` entry | rules lane |
| A4 duplicate upstream declaration: Pi declares `ServiceCall`, `ServiceMode`, `Op`, `FacetBundlePlatform`, `JsonlStorageOptions`, ... in two files | chord ~18, durable 2 | tool data (declaration merge) | rules lane |
| generic methods are package functions: `FacetEnvironment.provide/provideMany/use/observe/replicatedState`, `RemoteServiceProvider.provide/withdraw/replace/spawn/use/validateReplacement` | chord 24 | rule request (documented in lg-imla-5-chord-rule-requests.md) | rules lane |
| R8 JS `Error` base: `cause`, `name`, `stack` on `HostKeyChangedError`, `HostKeyUnknownError`, chord errors | env 2, chord 9 | rule request | rules lane |
| type-level TypeScript machinery: `Infer*`, `TelemetrySchemaSpan*`, `ExactTelemetryAttributes`, `HooksOf`, `MaybePromise`, `Draft` | telemetry 13, durable 4, chord 4, server 1 | designed-out needs owner approval (SCRUTINIZED) | owner |
| vitest adapters with no Go form (`StorageConformanceRunner`, `ExpectLike`, `createExpectAssertions`: `*testing.T` is the runner; gap-durable.md) | durable 3 | designed-out needs owner approval | owner |
| `not-exercised` library members | durable 162, env 94, mcp 46, telemetry 38, chord 7, codemode 9 | LEAD-DECISION-P1 (b): closes with the P1 rule plus the mutation-checked tests of gap-behavior-libs | ledger-autobind |

The 143 behavioural-evidence records of gap-behavior-libs (`test/parity/unit-evidence/gap-behavior-libs.json`) are the cited, mutation-checked Pi tests that rule P1 (b) asks for on the library rows.
