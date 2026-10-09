# Durable core: package layout and seams

Status: skeleton for the production core, 2026-10-07. Branch `dcore-skeleton` on staging. Lanes dcore-store, dcore-loop, dcore-session and dcore-host branch from it; the integrator (do-core-design) merges their READY milestones into the `dcore-integrate` staging branch. Architecture: [ADR-0001](ADR-0001-core-architecture.md). Host interface: [ABI.md](ABI.md). Correctness: [CONTRACT.md](CONTRACT.md).

## 1. Packages and owners

All paths are under `durable/core`. The skeleton adopts the packages the first lanes had already pushed (`history`, the gate under `durable/contract` and `contracttest`, and the hosts) and adds the seams between them. Core packages import only down this table; `purity_test.go` enforces the order and the rules of section 2.

| layer | package | owner | responsibility |
|---|---|---|---|
| 1 | `abi` | integrator | event, step and value types, wire codecs, `Builder`, `ID` (abi_id). Tooling beside it: `abi/spec` (normative tables, `abigen`), `abi/abitest` (event scripts), `abi/payload` (JSON payload shapes for hosts) |
| 2 | `jv` | dcore-session | ordered JSON values (`nil`, `bool`, `float64`, `string`, `[]any`, `*jv.Object`) with ECMAScript property order, JSON.parse and JSON.stringify fidelity; used by `rec`, `store` and `session` |
| 3 | `delta` | dcore-session | Chord document ops: the draft tracker that records them and `Apply`; `store` materialises base plus tail with it |
| 4 | `rec` | dcore-store | task, submission, conversation and document records: main da866ada shapes written, 1.0.4 read |
| 5 | `history` | dcore-store, with dcore-loop for context derivation | transcript arena and fixed-width index, the structural JSON scanner and JSON.stringify-faithful encoder, entry records and drafts, forks and fork document copies, head markers and edits, context derivation (rules 1-9), compaction range selection, summaries and token estimates, `Need` (the loads it asks for) |
| 6 | `store` | dcore-store | `sql_table`, Pi rows per commit, guarded metadata, cold open of live state, loads of non-live records and of `history.Need`, sidecar |
| 7 | `session` | dcore-session | Session line, scheduler, tasks, documents, inbox and submissions, forks, edits, the `pi.compaction` machine, sub-agents, reload, tx channel, api requests |
| 8 | `turn` | dcore-loop | `pi.generation` and `pi.tool` machines, rounds, usage, partial throttles, retries, deferred polls, recovery, model planes (wire plane behind the `wireplane` tag) |
| 9 | `.` (core) | integrator | event routing, read deferral, fatal and closed handles, `MemStats` |
| tooling | `cmd/corewasm` | integrator seeds, dcore-host owns | the WASI reactor binding (exports of ABI 4.1); the only package with package-level state |
| tooling | `sqlhost`, `driver`, `host/cf`, `probe`, `budget` | dcore-host | native host and `durable/harness` driver (D17), effect executors, Cloudflare DO host and pi-durable-compatible facade, probe core, size and startup budgets; `host/PROTOCOL.md` holds the payload bodies until the core owners ratify them |
| tooling | `contracttest`, `durable/contract` | integrator | the gate: `contracttest/gate.sh`, binding conformance (`contracttest/binding`), byte-level oracle vectors, and the Node CONTRACT tooling (capsql, detenv, tape, rowdiff, matrix, corpus) |

A lane edits only its packages. A seam change (a signature in another lane's package, or anything in `abi` or `core`) goes to `questions/<lane>.md`; the integrator changes the skeleton and merges it forward. `jv` and `delta` start in the session lane's tree as `session/jv` and `session/delta`; they move to `durable/core/jv` and `durable/core/delta` because `rec` and `store`, which sit below `session`, use them. `history` has two owners: dcore-store owns the arena, index, scanner, encoder and records; dcore-loop owns context derivation (`context.go`, `derive.go`) and the parts of compaction the generation reads (`compaction.go` thresholds and estimates); session owns nothing there but calls compaction planning.

## 2. Core rules

Every package above except the tooling rows (enforced by `purity_test.go`):

- standard library only; no `encoding/json`, `reflect`, `fmt`, `os`, `time`, `sync`, `context`, `unsafe`, `runtime`, `net`, randomness or logging packages;
- no `go` statements, channels or `select`; no I/O; no clock (time is `abi.Event.Now`); semantic randomness only through `session.Config.UUIDv7`;
- no package-level mutable state: a package-level `var` carries a doc comment saying it is read-only;
- the same source compiles natively, with `GOOS=wasip1` (Go 1.27.1) and with TinyGo 0.42 `-target=wasip1 -scheduler=none`.

Tests may use anything. Tests that need `encoding/json` or reflection carry `//go:build !tinygo`; a package whose tests compile under TinyGo is listed in `contracttest/tinygo-packages.txt`.

## 3. Seams

### 3.1 Events in, steps out (`abi`, `core`)

`core.Session.Step(abi.Event) *abi.Step` is the only entry point. Everything a step contains is written through the session's one `abi.Builder`: `Commit`, `Read`, `Notice`, `Effect`, `Timer`, `ClearTimer`, `Cancel`, `Reject`, `Fatal`. Effect, read and timer IDs are unique for the life of the handle. The builder refuses a read step that also commits or starts effects, so a handler loads before it writes.

An event payload aliases the host's input buffer; a package that keeps any part of it copies it.

### 3.2 Loads and read deferral (`history`, `store`, `core`)

A handler that needs state that is not resident gets it from `history` (which answers a `*history.Need` for a transcript range) or a `store.Ensure*` method. It hands a `Need` to `store.Load`; `store.ErrPending` means reads were emitted, and the handler returns that error unchanged, before any commit. `core` keeps the event (payload copied), routes the rows event to `store.Rows`, which feeds `history.Store.AddRow` and `SupplyBounds`, and re-delivers the deferred events in arrival order once every load is complete. Handlers must therefore be re-runnable from the top until their first commit.

### 3.3 Writes (`store`, `rec`, `session`)

One Pi commit is `store.Begin()`, the `Tx` write calls in the order pi-durable's code issues the equivalent `StorageWrite`s, then `store.Commit(builder, tx)`. Commit validates like `SqliteStorage.commit`, emits Pi's rows and the guarded metadata update, applies the commit to the in-memory state, and returns the sequence. `session.Tx` wraps `store.Tx` with pi-durable's `Tx` rules (spec §4); `turn` reaches it only through `session.Runtime.Commit`. Entry record bytes come only from `history.AppendEntryRecord`; the session appends each committed entry to `history.Store` with the sequence `store.Commit` returned. Other record bytes come only from `rec`. Sidecar writes come only from `store.Idle`.

### 3.4 Tasks (`session`, `turn`)

`session.TaskMachine` (`Kind`, `Version`, `Invoke`, `Deliver`, `Abort`) is the contract for built-in tasks; `turn.Machines()` returns `pi.generation` and `pi.tool`, and session registers `pi.compaction` itself. The scheduler owns reservation, step rules, reconciliation, handover and outcome cleanup (spec §5); a machine owns its phases. `session.Runtime` gives a machine the gated commit, effects and timers whose completions return to its `Deliver`, hook handlers, context views, settings and agent. A machine keeps only per-task state it can lose at any idle point.

### 3.5 Context (`history`)

`history.Store.Context(conv, at)` returns a `*history.View` (or a `Need`): active entries and the derived messages as references into the arena. `turn` splices `View.Message(i)` bytes into the `{messages}` of a `model_context` effect; session uses the same views for the `context` api request, compaction range selection (`PlanSelect`) and threshold estimates.

### 3.6 Record shapes

The core writes the record shapes of pi-durable main da866ada (task `startedAt`/`endedAt`, message `durationMs`) and reads 1.0.4 and newer stores. The oracles are pi-durable main for row identity and the TypeScript control lane (`durable-core-ts`, de1fac200) as a second implementation that already writes both shapes.

## 4. Gate

`durable/core/contracttest/gate.sh` runs format and vet, the generated-file check, native tests (including `purity_test.go`), the Go and TinyGo Wasm builds with sizes, TinyGo unit tests (`contracttest/tinygo-packages.txt`), binding conformance (native, Go Wasm, TinyGo Wasm and golden steps byte-equal for every `contracttest/binding/testdata/*.events` script), and, with `DCORE_CORPUS=1`, `make durable-contract`: the CONTRACT section 5 checks against pi-durable main da866ada through the capsql capture (statement stream, final store, model-context fingerprints, the crash handoff matrix at every commit) for every row of `durable/contract/corpus.toml`. A row is pass, fail, pending or skipped; pending never counts as pass, and the gate is green when no row fails. A lane runs the gate before every READY; the integrator runs it on every merge. A golden step file changes only in a reviewed commit (`DCORE_UPDATE_GOLDEN=1`).

## 5. Reuse

| source | into |
|---|---|
| wasm-spike core (872c81bf5): `splice.go`, `recover.go`, `wire.go` | `turn` |
| durable-s0 (58b037a23): `abi`, `probe`, `shim`, `contract` | `abi` (done), `probe` and `host/cf` (dcore-host), `durable/contract` (done) |
| durable-core-ts (de1fac200): TS core, verification harness, X1, handoff | second oracle for every row; record templates of both shapes |
| durable-perf (407c624b7): `contextCache` and its differential tests | `history` context derivation |
| PiG `durable/` port | semantic reference for `session` and `turn`; `chord/delta` for `delta` |
