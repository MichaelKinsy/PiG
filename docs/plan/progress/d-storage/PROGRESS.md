# d-storage progress

Lane: pi-durable 1.0.0 storage (`packages/durable/src/storage`, `src/testing`) → `durable/storage`, `durable/durabletest`.
Branch: `d-storage` (base `agg-100`; merges `d-foundation` through 5fd4cb226).

## Status: READY (reviewed in rev-d-storage; the cross-lane red is fixed)

## Source map

| Upstream | Go |
|---|---|
| `src/storage/memory.ts` | `durable/storage/memory.go`, `clone.go`, `internal/writes`, `internal/ops` |
| `src/storage/sqlite/{database,migrations,storage,index}.ts` | `durable/storage/sqlite/{database,migrations,storage,rows}.go` (portable, no driver) |
| `src/storage/sqlite/node.ts` | `durable/storage/sqlite/node/node.go` (the only package that links `modernc.org/sqlite`) |
| `src/storage/jsonl/{storage,index}.ts` | `durable/storage/jsonl/storage.go` (portable, consumer-owned `FileSystem`) |
| `src/storage/jsonl/node.ts` | `durable/storage/jsonl/node/node.go` |
| `src/testing/{index,storage-conformance}.ts` | `durable/durabletest/{durabletest,storage_conformance}.go` |
| `src/testing/storage-benchmark.ts` | `durable/durabletest/storage_benchmark.go` |

`docs/parity/PORT_MAP.md` has no `packages/durable/src` rows (the package is outside its denominator; `make port-map-drift` is clean), matching d-foundation.

## Test mapping (`test-mapping-v1.0.0.json`)

designed-out → ported: `memory-storage`, `jsonl-storage`, `sqlite-storage`, `sqlite-facade`, `sqlite-migrations`, `storage-runtime-boundary`. `storage.bench.ts` has no row; it is ported as Go benchmarks.

## Red → green list

All tests were ported from upstream with the same names and assertions. Source was written before most tests, so these tests are **not red-proven**. Each was proven by mutation instead: the listed mutation turned the named test red and was reverted.

| Test | Mutations caught |
|---|---|
| `durable/storage#TestMemoryStorageConformance` (23 shared cases) | no read clone; live-count threshold; ancestor `at` cap; no write clone; `resolveDocumentCopies` without clone |
| `durable/storage#TestMemoryStorageDoesNotExposeRetainedStateThroughAPreparedCommit` | `Writes()` returns the retained slice |
| `durable/storage/sqlite#TestSqliteStorageConformance` (plain + across reopen) | live-count; `Document` outside a read transaction |
| `durable/storage/sqlite#TestPicoSqliteStorage` (11) | ID adoption before settle; rollback `AggregateError`; indexed-string encoding |
| `durable/storage/sqlite#TestDurableSQLiteMigrations` (4) | newer-version guard; migrations outside a transaction |
| `durable/storage/sqlite/node#TestPortableSQLiteFacadeSettlement` (11, `testing/synctest`) | admitted-read drain before close; FIFO queue order; inactive transaction handle; rollback |
| `durable/storage/jsonl#TestJsonlStorageConformance` (plain + across reopen) | — (shared cases) |
| `durable/storage/jsonl#TestPicoJsonlStoragePublicationAndRecovery` (32) | retire reclamation; non-strict seq; authorizing main flush; unconfirmed tail truncation; optional confirmation; torn-line truncation; reclaim-temp flush; poison on main append; terminal task in main; empty-sidecar reclamation; sidecar seq order check; UTF-8 check; sidecar-state adoption |
| `durable/storage#TestDurableStorageRuntimeBoundaries` (4) | **red on first run**: the durable root loads `durable/env` (see Design notes); host import added to `durable/storage/jsonl` |
| `durable/storage#TestStorageBenchmarkExpectations`, `BenchmarkStorage{Read,Write,ReopenAndFirstExactRead}` | replay seed off by one |

Mutations that survived. Upstream tests do not observe these either: the same-sequence ordinal half of the sidecar order check (rev-d-storage added `TestJsonlStorageRejectsSameCommitSidecarRecordsOutOfOrdinalOrder` for it); a same-sequence ordinal tie in `isBeforeLatestBase`; the reclaimed-create content substitution; removal of a stale `.reclaim` file at recovery, which a later reclaim rewrite masks. The facade's rollback on a callback panic is a Go-only path, since upstream callbacks throw and that maps to the returned-error path, so no upstream test reaches it.

Commands: `CGO_ENABLED=0 go test ./durable/...`, `go vet ./durable/...`, `go tool golangci-lint run ./durable/storage/... ./durable/durabletest/...` (0 issues), `make port-map-drift`, `make test-inventory`, `make interface-go-drift`, `make check-scratch-paths`, `go test ./durable/storage/ -bench . -benchtime 3x`.

## Design notes

- **SQLite driver:** `modernc.org/sqlite` v1.53.0, which `go.mod` already required (also used by `test/parity/closure`). It is pure Go, so `CGO_ENABLED=0` builds and tests pass. `DatabaseSync` pins one `*sql.Conn` (`MaxOpenConns=1`), uses a `file:` URI with `mode=ro` for read-only opens, and caches prepared statements per connection.
- **Frozen writes:** Go has no `Object.freeze`, so `PreparedMemoryCommit.Writes()` returns a detached copy. The memory-storage test asserts detachment instead of a throw on mutation.
- **Prototype checks:** Go maps have no prototypes. The `__proto__` conformance assertions are reduced to keyed reads and detachment.
- **Lone surrogates:** records are encoded with `extensions/sdk/json` (WTF-8 lone surrogates and JavaScript string semantics). SQLite maps a cyclic value to "Converting circular structure to JSON".
- **JSONL content wire shape:** JSONL writes its own `{kind, version, value|ops}` struct. The foundation `DocumentContent` uses `omitempty` tags, which drop `value: {}` and `ops: []`, and recovery would then reject the line.
- **Unencodable data:** Go has no BigInt. The JSONL "serializes the complete candidate before I/O" test uses a channel as the unencodable value in place of `1n`.
- **Runtime boundary:** upstream follows relative imports inside `src` and forbids `node:` imports. Go has no type-only imports, and the root's `env.ExecutionEnv` use (upstream `types.ts:4`, `import type`) loads `durable/env`. The test therefore allows the portable `durable/env` but nothing beneath it, and forbids `os`, `os/exec`, `os/signal`, `syscall`, `net`, `net/http`, `database/sql`, `modernc.org/sqlite` and `path/filepath` imports in every durable package that the root, sqlite, env and jsonl graphs reach.
- **Promise ordering:** the facade test uses `testing/synctest` and a `future[T]` helper so that queued, in-flight and settled states are deterministic.
- **Op replay:** `internal/ops.ApplyBatches` calls `chord/delta.ApplyImmutableBatches`, since `durable.Op = delta.Op` (d-foundation 016b0a100).

## Stubs and cross-lane dependencies

- `durable/env/node` (`NodeExecutionEnv`) has no owner. `durable/storage/jsonl/node.NodeFileSystem` implements only the file operations that JSONL uses, with upstream `toFileError` code mapping and upstream `resolvePath` (`~`, `~/`, `file://`, `path.resolve`). Error messages are Go's OS error text, not Node's errno text. Replace it with `env/node` once that package is ported.
- The JSONL test's `instrumentedEnv` embeds `NodeFileSystem`, in the same way upstream's `InstrumentedEnv` extends `NodeExecutionEnv`.

## Blockers for review

- **chord/delta message:** fixed in rev-d-storage. `AssertValidOp` now reports `unknown op verb: ${String(op[0])}` as upstream `packages/chord/src/delta/index.ts:189` does, so the SQLite replay case passes with its upstream assertion.
- **Foundation types, now resolved upstream of this lane:** `TaskOutcome` and `EntryRecord.Model` now have JSON codecs (d-foundation), so terminal `result: null` and model entries round-trip through SQLite and JSONL.
