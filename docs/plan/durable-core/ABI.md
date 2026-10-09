# Durable core host ABI

Status: ABI 1, design, 2026-10-06. Applies to every Wasm bake-off lane ([BAKEOFF.md](BAKEOFF.md)), the production Wasm build, and the native Go embedding of the same core. Architecture: [ADR-0001](ADR-0001-core-architecture.md). Correctness: [CONTRACT.md](CONTRACT.md).

## 1. Identity, not negotiation

"ABI 1" names this revision of the document. At runtime the core exports `abi_id`, the hex sha256 of the normative tables in sections 4-8 (generated from the Go source of those tables, not hand-written), and the host compares it with the value it was built against. A mismatch is a startup error. There is no negotiation, feature probing or compatibility path: the core and its host shim ship in one bundle and upgrade together (AGENTS.md, Pig-owned format policy). Every change to a normative table changes `abi_id`; the document number changes when a human-visible revision is cut.

## 2. Starting point and what changed

The researcher's proposal (research notes 3) and the wasm-spike lane's measurement ABI (`configure/index_load/scan/submit/model_result/tool_result/recover`, one commit buffer per step) are the inputs.

| proposal | ABI 1 | why |
|---|---|---|
| import `commit(ptr,len)`, one call per commit | commits returned in the step (pull) | no re-entrancy; the host sees all commits before any effect (commit-before-effect is checkable); identical for native, Wasm and TS; the spike measured push 3.1-4.3 ms vs JS replay 2.9-5.1 ms per turn, so pull costs nothing measurable |
| import `sql(id,ptr,len)` registers text from the host | export `sql_table`, the core owns all SQL text | one source of truth for Pi's write statements; hosts cannot drift |
| params `0 null, 1 f64, 2 text` | `0 null, 1 i64, 2 f64, 3 text, 4 blob` | exact integers for native hosts; BLOB for sidecar chunks (the spike already added it) |
| import `now() -> f64` | `now` is an argument of every event | the core is deterministic and replayable; capture runs drive a virtual clock |
| import `ctx(role,text,args)` | `model_context` effect payload, plus `inspect` | the model request is an effect with an ID, not a side channel |
| `create`, `open_begin`, `open_usage`, `open_entries` | `open` event plus read requests | the core decides what it needs (sidecar, head marker, delta); the host does not hard-code bench queries |
| `submit(id,text) -> effect code` | `submit` event, any number of effects with IDs | concurrency: parallel tools, sub-agents, several conversations |
| `model_call`, `model_answer`, `tool_result` without correlation | `model_event`, `model_bytes`, `tool_progress`, `tool_done` with effect IDs | completions arrive in any order |
| `turn_native` | bench plane only (section 9) | an in-core scripted model is a benchmark variant, never production |
| `ctx_dump` | `inspect` event | general test plane |

Gaps the proposal left, now covered: streaming deltas and the partial throttle (sections 5, 6); timers and alarms; hooks, custom task phases and extension transactions (section 8); multiple conversations and multiple sessions per instance; cancellation; read requests; publication to observers; error and trap handling; buffer ownership; binding conformance between Wasm and native. The design review added: pi-ai `uuidv7()` as a host import, separate from WASI `random_get` (section 4.2); the 1.0.4 shape of the model context (section 6); prompt-section rendering, deferred polling and in-commit host callbacks as effects; acknowledged tool `details()`; and the map from every host-facing runtime method to a channel (section 8).

## 3. Logical model

A host creates a session handle per Durable Object (or per Session natively) and drives it with events. Each event returns one step.

```text
step := status, commits[], reads[], notices[], effects[]
```

Host loop invariants:

1. Never call the core re-entrantly (not from inside a commit, read or notice handler).
2. Apply every commit of a step, in order, each in its own transaction (DO `transactionSync`; Node `BEGIN IMMEDIATE`/`COMMIT`; Go `*sql.Tx`). Any commit failure is fatal to the handle (section 4.6).
3. A step with reads has no commits and no effects. The host runs every read and answers all of them in one `rows` event before delivering any other event.
4. Deliver notices after the step's commits are applied.
5. Start effects only after the step's commits are applied. In a DO the output gate additionally holds outbound network until those writes are durable.
6. Effect IDs are unique for the life of a handle. When a handle is discarded, the host cancels its in-flight effects and drops their late completions.

## 4. Wasm binding

Module: WASI preview 1 reactor (`GOOS=wasip1 GOARCH=wasm -buildmode=c-shared`; TinyGo `-target=wasip1 -buildmode=c-shared`). All integers little-endian. Pointers are `u32` offsets into the core's exported `memory`.

### 4.1 Exports

| export | signature | meaning |
|---|---|---|
| `_initialize` | `()` | WASI reactor init, once per instance |
| `abi_id` | `() -> ptr` | 64 ASCII hex bytes |
| `sql_table` | `() -> ptr` | `u16 n` then `n` × (`u32 len`, UTF-8 SQL); index = statement ID |
| `session_new` | `() -> u32` | new handle (≥ 1) |
| `session_free` | `(h)` | release all memory of a handle |
| `in_reserve` | `(n u32) -> ptr` | core-owned input buffer of ≥ `n` bytes, valid until the next `step` |
| `step` | `(h, kind u32, len u32, now f64) -> u32` | process the event whose payload is in the input buffer; returns the step length |
| `out_ptr` | `() -> ptr` | the step bytes; valid until the next export call |
| `mem_stats` | `(h) -> ptr` | 8 × `u64`: arena, index, derived, documents, scratch, total live, heap in use, linear memory size |

No other exports are part of ABI 1. Bench and test exports are build-tagged (section 9).

### 4.2 Imports

Imports from `wasi_snapshot_preview1` are a closed set, and the host provides all of it; a core imports any subset. A Go `GOOS=wasip1 -buildmode=c-shared` reactor imports `args_get`, `args_sizes_get`, `clock_time_get`, `environ_get`, `environ_sizes_get`, `fd_write`, `poll_oneoff`, `proc_exit`, `random_get` and `sched_yield` (measured on a minimal reactor with Go 1.26.1 in the design review and again with Go 1.27.1 by S0). Linking package `os` or `fmt` adds `fd_close`, `fd_fdstat_get`, `fd_fdstat_set_flags`, `fd_prestat_dir_name`, `fd_prestat_get` and `fd_read`; the host stubs them. The set is a table in `abi/tables.go` and part of `abi_id`; `tools/wasm-imports.mjs` fails on a module that imports anything outside it. S0 has not measured TinyGo, Rust or Zig modules (V14): the lane that builds one runs the tool and adds what it reports. `fd_write` is a debug log to stderr only; `proc_exit` is treated as a trap; `clock_time_get` and `random_get` serve the toolchain runtime only (Go seeds its runtime RNG from `random_get` at start, so its draws are toolchain-dependent) and never influence semantics.

Semantic randomness comes from one non-WASI import:

| import | signature | meaning |
|---|---|---|
| `pig.uuidv7` | `(dst ptr)` | write the 36 ASCII bytes of one pi-ai `uuidv7()` value; called only for the provider document's `sessionId` (`src/harness/provider.ts:18`) |

The host owns the generator because pi-ai keeps `uuidv7()`'s clock and sequence state per process (`packages/ai/src/utils/uuid.ts`), across Harness instances and reopens in that process: a JS host calls pi-ai's own `uuidv7()`, a native host a process-level Go port of it, and capture runs return the values recorded on the effect tape (CONTRACT section 2.2). The call is a pure data source: the host must not call back into the core from it. Lane-specific imports (`kernel.*`, `hist.*`) are defined in section 10.

### 4.3 Values

```text
value := u8 tag, payload
  0 null
  1 i64    8 bytes   (|v| ≤ 2^53 unless the column is TEXT)
  2 f64    8 bytes
  3 text   u32 len, UTF-8 (always well-formed; JSON escapes carry any lone surrogate)
  4 blob   u32 len, bytes
```

A DO host binds `text` as a JS string (`TextDecoder`), `blob` as an `ArrayBuffer`, `i64` as a number. Statements that read record columns use `CAST(record AS BLOB)` so rows return bytes without a UTF-16 round trip; the core never asks the host to decode a record.

### 4.4 Step layout

```text
step    := u32 size, u8 status, u16 nCommits, u16 nReads, u16 nNotices, u16 nEffects,
           commit*, read*, notice*, effect*, [status != 0: u32 len, error JSON]
commit  := u32 len, i64 seq, u16 nStmts, stmt*
stmt    := u16 sqlId, u8 nParams, value*
read    := u32 readId, u16 sqlId, u8 nParams, value*
notice  := u8 kind, u32 len, payload
effect  := u32 effectId, u8 kind, u32 len, payload
status  := 0 ok | 1 rejected (no state changed; error is Pi's error name and message) | 2 fatal (discard the handle)
```

`seq` is the `next_seq` value the commit consumes, or `-1` for a sidecar-only commit; hosts use it for traces and assertions only.

### 4.5 Rows (answer to reads)

```text
rows-event := u16 nReads, (u32 readId, u32 nRows, u8 nCols, value{nRows × nCols})*
```

### 4.6 Failures

- Commit failure: the core validates everything pi-durable's storage validates (ID claims, document consistency) from its own state before emitting a commit, so a failing commit means the store and the core disagree. The host treats it as fatal: discard the handle, cancel its effects, reopen on the next event (D16).
- Single-writer guard: the core's `durable_metadata` update carries `AND next_seq = ?`. The host checks that it changed exactly one row (DO `cursor.rowsWritten`, SQLite `changes()`); otherwise fatal.
- Trap: discard the instance and every handle on it.

## 5. Events

Payload layouts; "JSON" is UTF-8 JSON.

| kind | name | payload | notes |
|---|---|---|---|
| 1 | `open` | JSON: harness options the core needs (settings, agent defaults, models catalogue entries in use, registry snapshot: extension order, tool schemas and replay policies, sections, task definitions' names and versions), `core_id` expectation, sidecar mode | answered by a read step |
| 2 | `rows` | section 4.5 | only after a read step |
| 3 | `submit` | JSON `{conversationId, type, content or entry, requestId, whenBusy}` | §6 admission |
| 4 | `model_event` | `u32 effectId`, JSON array of pi-ai `AssistantMessageEvent` | context plane; batching allowed; the final `done`/`error` event carries the final message |
| 5 | `model_bytes` | `u32 effectId`, `u8 phase` (0 response head JSON, 1 body chunk, 2 end, 3 transport error JSON), bytes | wire plane |
| 6 | `tool_progress` | `u32 effectId`, `u8 kind` (0 output chunk, 1 details JSON, 2 diagnostic JSON), `u32 skipped`, `u32 waitId` (kind 1 only; 0 otherwise), bytes | throttled into `pi.live` by the core; a details update answers with a `progress_ack` notice once the commit that includes it is applied, because Pi's `api.details()` resolves only then (`src/harness/tool.ts:211-216`, `output.ts:292`) |
| 7 | `tool_done` | `u32 effectId`, `u8 outcome` (0 result, 1 thrown), JSON | §8.4 |
| 8 | `hook_done` | `u32 effectId`, `u8 outcome` (0 value, 1 thrown), JSON | completes a `hook`, `env`, `section`, `deferred` or `callback` effect; one handler per `hook` effect (section 6) |
| 9 | `phase_done` | `u32 effectId`, `u8 outcome`, JSON | custom task handler settled |
| 10 | `tx` | `u32 txId`, JSON op | section 8 |
| 11 | `timer` | `u32 timerId` | early or duplicate wakes are no-ops |
| 12 | `abort` | JSON `{taskId}` or `{conversationId}` | `abortTask`, conversation abort |
| 13 | `registry` | JSON registry snapshot | extension reload (§7.5) |
| 14 | `api` | `u32 requestId`, JSON | Harness reads that need core state (context view, usage, task graph); answered by an `api_result` notice |
| 15 | `close` | none | seals admission (§5.1); the step cancels effects; the handle may then be freed |
| 16 | `inspect` | JSON query | test plane: context view and fingerprint, index dump, memory; never mutates |

## 6. Effects

| kind | name | payload | host action |
|---|---|---|---|
| 1 | `timer` | `u32 timerId`, `f64 at`, `u8 durable` | volatile: `setTimeout`; durable: keep the object alarm at the earliest durable `at` |
| 2 | `timer_clear` | `u32 timerId` | |
| 3 | `model_context` | JSON `{model:{provider,modelId}, context, options}`. `context` is exactly the pi-ai `Context` pi-durable 1.0.4 passes, `{messages}` (`src/harness/generation.ts:402`): the system prompt and tool declarations are positional `pi.system` messages inside `messages` (§7.4), not separate fields. `messages` is spliced from stored bytes, after any `beforeRequest` replacement. `options` is the request's `SimpleStreamOptions` without `signal` (`reasoning`, the provider `sessionId`, the pinned stream options). When the request's message list is the list sent by an earlier `model_context` effect of the same conversation plus appended messages, the payload is `{model, extends: effectId, append: [...], options}` instead; `extends` names that effect's list exactly as sent | run pi-ai `streamSimple` (or the scripted model) on the full or extended message list the host keeps per conversation; send `model_event`. The host may drop a kept list once a later effect extends it or the conversation unloads. The delta form spares a JS host from parsing the whole context on every call |
| 4 | `model_http` | JSON head `{method, url, headers, auth}` then a body recipe (section 10.2) | add credentials for `auth`, `fetch`, stream `model_bytes` |
| 5 | `cancel` | `u32 effectId` | abort the effect's signal; its completion may still arrive and is handled per §8.3 |
| 6 | `tool` | JSON `{taskId, conversationId, toolName, callId, arguments, outputWindow, replay}` | run `execute` with an api that sends `tool_progress`; then `tool_done` |
| 7 | `hook` | JSON `{name, handler, conversationId, taskId, payload}` | call the handler at index `handler` of the registry snapshot; the core runs the chain rules of §7.2 |
| 8 | `phase` | JSON `{taskId, kind, phase, record, invocation}` or `{..., abort: true}` | run the custom task's phase or abort handler with a runtime facade over `api` and `tx` |
| 9 | `env` | JSON `{conversationId}` | call `HarnessOptions.env`; result as `hook_done` |
| 10 | `liveness` | `f64 at` or `-1` | watchdog alarm on, or off when nothing is live (ADR D7) |
| 11 | `section` | JSON `{conversationId, taskId, section, input}`: `section` indexes the agent's resolved `sections` (extension sections, then `instructions`), `input` is the `PromptInput` data (`shown`, the agent, the conversation) | call `PromptSection.render()` with a `DocumentReader` over `api`; the rendered string or `undefined` returns as `hook_done`. Preparation calls it for every section on every request (§7.4 step 3; `src/harness/types.ts:250-255`) |
| 12 | `deferred` | JSON `{op: "fetch" or "cancel", model, handle}` | call `models.fetchDeferred()` or `models.cancelDeferred()` (§8.3 `poll`, `src/harness/generation.ts:229,259`); the message or acknowledgement returns as `hook_done` |
| 13 | `callback` | JSON `{name, txId, payload}` | run host code that Pi calls inside an open commit callback, with the §8 channel bound to `txId`: `HarnessOptions.conversationCreated` (`src/harness/types.ts:457`), a conversation's `init`, and an extension document definition's `initial`, `migrate` or `checkpointWhen` (`src/documents.ts:87-89`). The commit stays open until `hook_done`; built-in document definitions are core code and never use this effect |

## 7. Notices

| kind | name | payload |
|---|---|---|
| 1 | `published` | JSON: committed document ops by document, appended entry IDs, task status changes, submission settlements; drives `Submission.wait`, watches and Chord clients (§9) |
| 2 | `api_result` | `u32 requestId`, JSON |
| 3 | `tx_result` | `u32 txId`, JSON (`ready`, `waiting` for the line, an op's result, or `rejected` with Pi's error) |
| 4 | `report` | JSON for `HarnessOptions.onReport` |
| 5 | `sidecar` | JSON `{conversationId, rows}`: the core wrote a sidecar chunk in this step's commits (diagnostics only) |
| 6 | `progress_ack` | `u32 waitId`, `u8 outcome` (0 committed, 1 rejected), JSON error when rejected: settles the promise of a tool's `api.details()` |

Sidecar writes, including the `CREATE TABLE IF NOT EXISTS` on first use, are ordinary commits on `pig_*` statements outside Pi's `next_seq` sequence; their `seq` is `-1` (ADR D6).

## 8. Extension transaction channel

`runtime.commit(tx => ...)`, `session.commit(...)` and other host-side commits use one protocol:

1. `tx {op: "begin", invocation?, conversationId?}` → `tx_result ready` with a `txId`, or `waiting` when the Session line is held; the host awaits the `ready` notice that a later step delivers.
2. `tx {op: ..., ...}` for each `Tx` operation (`conversation`, `doc`, `appendEntry`, `createTask`, `placeSubmission`, `settleSubmission`, `entry`, ...). Document mutations travel as Chord `Op[]` produced by the host's draft proxy. The core enforces `ReadAfterWrite` and every other §4 rule and answers each op with `tx_result`.
3. `tx {op: "end", state?}` with the callback's returned next task state, or `{op: "abort", error}` → a step carrying the commit, or `rejected`.

The line stays held from `begin` to `end`, across host awaits, as §4 requires; events for other work that arrive meanwhile are queued inside the core and processed after `end`. This plane is JSON because extension commits are not on the hot path; built-in tasks never use it.

Host code reaches the core only through these channels. Every method of the facades Pi hands to host code (`TaskRuntime`, `src/types.ts:170`; `ToolExecutionApi`, `src/harness/types.ts:166-204`; `HookApi`, `src/harness/types.ts:605-610`) maps to one of them:

| method | channel |
|---|---|
| `commit(tx => ...)` | `tx` (section 8), bound to the calling task |
| `memo(name, candidate)` | `api` request; the core commits it as one gated commit (§5.1) and answers with `api_result` after the commit is applied |
| `memo(name)`, `snapshot`, `snapshotAsOf`, `getTask`, `outcomes`, `entry`, `context`, `conversation`, `agent()` | `api` request answered by `api_result` from committed state |
| `waitForTask`, `watchDoc` and other observations | `api` request; the core answers when the awaited state is published, and `published` notices drive watches |
| `createTask` | `tx` with a single `createTask` op; the host computes the definition's `initial(input)` before sending it |
| `sleep(until)` | `api` request; the core arms a durable `timer` and answers at the wake |
| `output`, `diagnostic`, `details` | `tool_progress` (`details` waits for `progress_ack`) |
| `env` | the `env` effect, issued by the core before `execute` (§8.4) |
| `report` | handled in the host; the core's own reports arrive as `report` notices |
| `signal` | the host's `AbortController` for the effect, aborted on `cancel` |

### 8.1 The `api` request table

An `api` event is `u32 requestId` then JSON `{op, ...args}`. The answer is an `api_result` notice, `u32 requestId` then JSON `{ok: value}` or `{error: {name, message}}`. A document address `doc` is `{scope, definition, conversationId?, taskId?, key?}`. The table is frozen with ABI 1 and part of `abi_id` (`apiRows` in `abi/tables.go`); an operation outside it is rejected.

| op | args | result | answered |
|---|---|---|---|
| `memo.get` | taskId, name | the memo value or null | at once |
| `memo.put` | taskId, name, candidate | the durable winner | after the memo's commit is applied |
| `snapshot` | doc | the document state or null | at once |
| `snapshotAsOf` | doc, seq | the document state at seq or null | at once |
| `watch.open` | doc or graph, watchId | the first frame | at once; later frames arrive as `published` notices carrying watchId |
| `watch.close` | watchId | null | at once |
| `getTask` | taskId | the task record or null | at once |
| `waitForTask` | taskId | the settled receipt | when the task is terminal and published |
| `outcomes` | taskIds | the outcomes in order, or an error naming the missing one | at once |
| `entry` | conversationId, entryId | the entry record or null | at once |
| `context` | conversationId, at | the ContextView | at once |
| `conversation` | conversationId | the conversation handle data or null | at once |
| `agent` | conversationId | the resolved agent | at once |
| `sleep` | taskId, until | null | at the wake of the durable timer the core arms |
| `usage` | none | the UsageState of the Session | at once |
| `taskGraph` | none | the TaskGraph | at once |

`HarnessOptions.onReport` and `TaskRuntime.report` stay in the host; `commit` and `createTask` use the `tx` channel.

## 9. Bench and test plane

Built only with the `bench` build tag and never shipped:

| export | meaning |
|---|---|
| `bench_stub(h, effectId) -> u32` | answer a pending `model_context` effect with durable-bench's scripted reply computed over the core's index (the "core stub" variant); returns a step |
| `bench_fingerprint(h, conversationId) -> ptr` | durable-bench's fingerprint input for the conversation |

Reports must label every number produced with `bench_stub` (BAKEOFF.md section 4).

## 10. Lane-specific bindings

### 10.1 Host-held history (lane L3)

The arena lives in the host. The core keeps only the index. Imports:

| import | signature | meaning |
|---|---|---|
| `hist.copy` | `(conv u32, rec u32, off u32, len u32, dst ptr) -> u32` | copy part of a stored record into core memory (scanning, rare reads) |
| `hist.drop` | `(conv u32)` | the conversation unloaded |

The host appends to its store from the `rows` it served and from every `entries` insert it applies, so the core never sends record bytes twice. Cold scanning streams records through a reused scratch buffer, so core memory stays flat.

### 10.2 Body recipe (wire and context planes, all lanes)

```text
recipe  := u32 nSegments, segment*
segment := u8 0, u32 len, bytes                         literal from the core
         | u8 1, u32 conv, u32 rec, u32 off, u32 len     a stored record range (L3: host store; others: never used)
```

Core-held lanes emit literals only. L3 emits record references so a provider body is assembled by the host without copying through the core.

### 10.3 Byte kernel (lane L4)

A second Wasm module (Rust or Zig, `wasm32`, SIMD128 allowed) owns the arena in its own memory. The Go core imports it; the host wires the kernel instance's exports to the core's `kernel.*` imports.

| import | signature | meaning |
|---|---|---|
| `kernel.append` | `(conv, ptr, len) -> rec` | copy a new record from core memory |
| `kernel.load` | `(conv, n) -> ptr` | kernel buffer the host fills with `n` bytes of `[u32 len, record]*` rows directly |
| `kernel.index` | `(conv, fromRec, dst, cap) -> n` | write fixed-width index records into core memory |
| `kernel.splice` | `(conv, recipePtr, recipeLen, dst, cap) -> len` | assemble a body |
| `kernel.hash` | `(conv, rec) -> u64` | sidecar prefix check (ADR D6) |
| `kernel.drop` | `(conv)` | unload |

The kernel's index record layout is the core's (the section 12 golden vectors pin it).

## 11. Native Go binding

The core is a Go package; the Wasm binding is a thin encoder over it. Shape (names are the proposal; the implementing lane may adjust them before ABI 1 is frozen by its first merge):

```go
package core

const ABIID = "..."                        // same value as the Wasm abi_id export
func SQLTable() []string

type Session struct{ /* unexported */ }
// NewSession creates a handle. uuidv7 returns one pi-ai uuidv7() value from the host's process-level generator (section 4.2).
func NewSession(uuidv7 func() string) *Session
// Step processes one event. The returned Step and every slice in it are valid until the next Step call.
func (s *Session) Step(ev Event) *Step

type Event struct {
	Kind    EventKind
	Now     float64
	ID      uint32  // effect, tx, timer or request ID, by kind
	Payload []byte
	Rows    []ReadRows
}
type Step struct {
	Status  Status
	Err     *Error
	Commits []Commit
	Reads   []Read
	Notices []Notice
	Effects []Effect
}
type Commit struct {
	Seq   int64
	Stmts []Stmt
}
type Stmt struct {
	SQL    uint16
	Params []Value
}
type Value struct {
	Kind  ValueKind // Null, Int, Float, Text, Blob
	Int   int64
	Float float64
	Bytes []byte
}
```

The native host (proposed `durable/core/sqlhost`) owns one goroutine per Session that calls `Step`, applies commits with prepared modernc statements, answers reads, and runs effects in owned goroutines with `context.Context` cancellation that post completions back to the owner (ADR D17). No conversion to bytes happens natively.

The production core implements this binding as `durable/core` (`core.NewSession`, `core.Session.Step`, `core.SQLTable`, `core.ABIID`) over the types of `durable/core/abi`; its packages and seams are in [LAYOUT.md](LAYOUT.md).

## 12. Conformance

- Binding conformance: event scripts in `durable/core/abi/testdata/*.events` run through the native `Step` and through the Wasm module under Node; the native steps, encoded with the Wasm encoder, must equal the Wasm step bytes exactly.
- Golden vectors: one script per event and effect kind, plus the bench workload's first measured turn, with expected steps checked in. `abi_id` covers the normative tables of sections 4-8, not the vectors; a vector changes when core behaviour changes, and a vector change that needs a table change also changes `abi_id`.
- Host conformance: the shim test drives a scripted session against `node:sqlite` and Miniflare and asserts the host loop invariants of section 3 (commit-before-effect ordering, reads answered first, single-writer guard, effect cancellation after discard).
- TS control lane: implements sections 3, 5, 6 and 7 natively in TypeScript (no byte encoding) and runs the same event scripts through a JSON form of the vectors.
