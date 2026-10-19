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
3. A step with reads has no effects. The host applies its commits, in order, before it runs its reads, then runs every read and answers all of them in one `rows` event before delivering any other event. A core never adds a commit after a read in one step. Cold open uses this for Pi's schema prelude (`applySqliteMigrations`, `src/storage/sqlite/migrations.ts`): the open step commits `CREATE TABLE IF NOT EXISTS durable_schema` and reads the version (a missing row reads as version 0; the `INSERT OR IGNORE` of the row opens the migration commit, so every row of a new database is written in one transaction, as in Pi's commit 0; unlike Pi, the empty `durable_schema` table is created in the transaction before it, and a store left with that table and no row reads as version 0 and is migrated on the next open), so the core finds its schema by identifier, never by a table name held as data, and an adapter that renames Pi's tables in the SQL text (a table prefix) renames every name the core relies on.
4. Deliver notices after the step's commits are applied.
5. Start effects only after the step's commits are applied. In a DO the output gate additionally holds outbound network until those writes are durable.
6. Effect IDs increase for the life of a handle: each step's effects carry higher IDs than every effect before them, so a host detects a reissued ID with the last ID it started and keeps no per-effect state. When a handle is discarded, the host cancels its in-flight effects and drops their late completions.

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
| `in_reserve` | `(n u32) -> ptr` | core-owned input buffer of ≥ `n` bytes. The pointer stays valid until the next `in_reserve`, so a host reserves once and calls again only for a payload larger than its last reservation |
| `step` | `(h, kind u32, len u32, now f64, origin u32) -> ptr` | process the event whose payload is in the input buffer, a host call made in the async context `origin` (section 5.4; 0 for none); returns the address of the step's parts: `u32 n`, then `n` × (`u32 ptr`, `u32 len`). The step is the concatenation of the `n` byte ranges; its first `u32` is its size (the step is `size + 4` bytes). A notice or effect payload of 64 KiB or more is a part of its own, where the core built it, so a model request (the whole transcript) is not copied into the output buffer; a step without one is one part, which the host decodes in place. Valid until the next export call |
| `hop` | `(h, effectId u32, now f64) -> ptr` | process the `hopped` event of hops effect `effectId`, which carries no payload to copy. Returns 0 when the step has no commit, read, notice, effect or error. Returns the address of a part list with zero parts, then `u32 effectId`, `u32 n`, when the step's only output is one hops effect that carries no commit: the host opens that chain without decoding a step. Returns the address of a step, as `step` does, otherwise. Valid until the next export call. A host may send every `hopped` through `step` instead |
| `mem_stats` | `(h) -> ptr` | 8 × `u64`: arena, index, derived, documents, scratch, total live, heap in use, linear memory size. The binding fills the last two from its runtime: heap in use is the live heap after a full collection the call runs (so the call is a diagnostic, never on a turn's path); a Wasm binding leaves the linear memory size 0 for the host to read `memory.buffer.byteLength`, and the native binding (cmd/corenative) reports what its runtime obtained from the OS. The session counters are 0 for an unknown handle; the heap is the instance's |
| `alloc_stats` | `() -> ptr` | 3 × `u64`: bytes allocated, allocations and completed collections since the instance started (the runtime's TotalAlloc, Mallocs and NumGC). It runs no collection, so a host may read it around every step to count a step's allocation; the counters are the instance's, not a session's |

No other exports are part of ABI 1. Bench and test exports are build-tagged (section 9).

### 4.2 Imports

Imports from `wasi_snapshot_preview1` are a closed set, and the host provides all of it; a core imports any subset. A Go `GOOS=wasip1 -buildmode=c-shared` reactor imports `args_get`, `args_sizes_get`, `clock_time_get`, `environ_get`, `environ_sizes_get`, `fd_write`, `poll_oneoff`, `proc_exit`, `random_get` and `sched_yield` (measured on a minimal reactor with Go 1.26.1 in the design review and again with Go 1.27.1 by S0). Linking package `os` or `fmt` adds `fd_close`, `fd_fdstat_get`, `fd_fdstat_set_flags`, `fd_prestat_dir_name`, `fd_prestat_get` and `fd_read`; the host stubs them. The set is a table in `abi/tables.go` and part of `abi_id`; `tools/wasm-imports.mjs` fails on a module that imports anything outside it. S0 has not measured TinyGo, Rust or Zig modules (V14): the lane that builds one runs the tool and adds what it reports. `fd_write` is a debug log to stderr only; `proc_exit` is treated as a trap; `clock_time_get` and `random_get` serve the toolchain runtime only (Go seeds its runtime RNG from `random_get` at start, so its draws are toolchain-dependent) and never influence semantics.

Semantic randomness comes from one non-WASI import:

| import | signature | meaning |
|---|---|---|
| `pig.uuidv7` | `(dst ptr)` | write the 36 ASCII bytes of one pi-ai `uuidv7()` value; called only for the provider document's `sessionId` (`src/harness/provider.ts:18`) |

Committed state the core does not hold comes from one more non-WASI import:

| import | signature | meaning |
|---|---|---|
| `pig.read` | `(req ptr, reqLen u32, dst ptr, cap u32) -> u32` | run one read statement of `sql_table` synchronously and answer it in the call. `req` is `u16 sqlId, u8 nParams, value*` (a `read` of section 4.4 without its ID). The answer is `u32 nRows, u8 nCols, value{nRows*nCols}` (one answer of the rows event without its ID). The host writes it at `dst` and returns its length; when the length exceeds `cap` it writes nothing and the core calls again with a buffer of that length |

The core holds the live state it opens (section 3) and reads anything else when a step needs it: a terminal task, a settled submission, a submission by request ID. Pi awaits those reads on Storage; the core models their await time with `hops` (section 5.3), so the read itself takes no host time the Session can observe: the host answers inside the call, from the store state every commit it applied so far leaves, and runs no other work in between. A record the core wrote is in its own state, so a read never needs a commit the host has not applied yet. Like `pig.uuidv7`, the call is a pure data source: the host must not call back into the core from it. A read that fails traps the step (section 4.6).

**Context reads.** Three statements of `sql_table` are not SQL: the host answers them from the contexts it derives for model requests (section 10.2), through the same `pig.read` call. Their texts name functions SQLite lacks (`pig_context_summary`, `pig_context_messages`, `pig_context_release`), so a host that ran one as SQL fails loudly. The core names a context by its bounds, the newest entry (`tail`) and the newest head marker at or below it, and never reads the transcript through `pig.read`: it asks the host for what request preparation needs.

| statement | parameters | answer |
|---|---|---|
| `pig_context_summary` | conversation id, `tail`, head marker id (0: none), the marker's `head`, 1 when the estimate is wanted else 0 | one row, one BLOB: JSON `{tail, head, entries?, system, tokens}`. `head` is the marker id or null. `entries` is `[[id, kind]...]` for every active entry and is present only with a marker. `system` is the context's system messages, in order. `tokens` is `estimateContext(view, [])` of `src/harness/compaction.ts` (0 when not wanted) |
| `pig_context_messages` | conversation id, `tail`, marker id, the marker's `head` | one row, one BLOB: the JSON array of the context's model messages (what the `beforeRequest` hooks receive) |
| `pig_context_release` | conversation id | no rows: the scheduler dropped the conversation's kept context (idle for `contextRetentionMs`), so the host drops its range |

The host keeps the newest range each conversation reads (a read through an older entry leaves it, as Pi's scheduler does in `runtime.context`), so the model request that follows a summary read of the same bounds derives nothing again. A host that does not find `tail` in storage fails the read, and so does one whose stored marker has another `head` than the one the core named (the Go host also checks that the marker is the newest at or below `tail`). A core with no reader holds every record and derives the context itself. Compaction range selection and overflow classification still derive the view in the core, and only when the estimate crosses a threshold.

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
           commit*, read*, notice*, effect*, order, [status != 0: u32 len, error JSON]
commit  := u32 len, i64 seq, u16 nStmts, stmt*
stmt    := u16 sqlId, u8 nParams, value*
read    := u32 readId, u16 sqlId, u8 nParams, value*
notice  := u8 kind, u32 origin, u32 len, payload
effect  := u32 effectId, u8 kind, u32 origin, u32 len, payload
order   := u16 n, (u8 region, u16 index)*      n is 0 (all notices, then all effects) or nNotices + nEffects; region 0 notice, 1 effect
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
- Trap: the instance is poisoned and nothing of the failed step was applied (commits apply after the step returns). One instance serves one object, so the host discards the instance and its handle. A host that can reopen (the Durable Object session) drops it at once, cold-opens a new instance from the store (the core reconciles and resumes the work the dead session held) and feeds the client request that was waiting (`api`, `inspect`, `submit`, `abort`: it carries its own request ID) to the new session once. Effect completions and timers are not fed: their IDs belong to the dead session, and the reopen re-derives their work. Other client requests in flight fail. A second trap on the fed request, a trap in the reopen, or a third consecutive trap is final: the caller gets the error and the object alarm retries the reopen with a doubling delay (1 s, 2 s, ... 60 s). Every trap report carries the last 32 events the handle delivered (kind, payload size, clock input).

## 5. Events

Payload layouts; "JSON" is UTF-8 JSON.

| kind | name | payload | notes |
|---|---|---|---|
| 1 | `open` | JSON: harness options the core needs (settings, agent defaults, models catalogue entries in use, registry snapshot: extension order, tool schemas and replay policies, sections, task definitions' names and versions), `core_id` expectation, sidecar mode | answered by a step that commits the schema prelude and reads the schema version (section 3, invariant 3) |
| 2 | `rows` | section 4.5 | only after a read step |
| 3 | `submit` | JSON `{conversationId, type, content or entry, requestId, whenBusy}` | §6 admission |
| 4 | `model_event` | `u32 effectId`, JSON array of pi-ai `AssistantMessageEvent` | context plane; batching allowed; the final `done`/`error` event carries the final message |
| 5 | `model_bytes` | `u32 effectId`, `u8 phase` (0 response head JSON, 1 body chunk, 2 end, 3 transport error JSON), bytes | wire plane |
| 6 | `tool_progress` | `u32 effectId`, `u8 kind` (0 output chunk, 1 details JSON, 2 diagnostic JSON), `u32 skipped` (`ShellOutputSkip.bytes`), `u32 waitId` (kind 1 only; 0 otherwise), bytes; when `skipped` is non-zero the bytes start with `u32 metaLength` and the JSON `{bytes, newlines, endsWithNewline}` (the full `ShellOutputSkip`), then the chunk | throttled into `pi.live` by the core; a details update answers with a `progress_ack` notice once the commit that includes it is applied, because Pi's `api.details()` resolves only then (`src/harness/tool.ts:211-216`, `output.ts:292`) |
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
| 17 | `resume` | none | the host's `Harness.resume()`: scheduling that waits for the first progress-asking call (Pi `harness.ts` `resume`) starts; idempotent |
| 18 | `hopped` | `u32 effectId` | the `hops` chain ran its hops (section 5.3); the core runs the work parked on it |
| 19 | `seal` | none | `Harness.close()` was called: the core does what Pi's close listeners do synchronously (`session.ts:352-366`, scheduler `#seal`: closing flag, every invocation's signal) and keeps running admitted work; the terminal `close` (15) follows once caller transactions have ended. A close() called from user code inside a step job (the registry reader at `scheduler.ts:955`) reaches the core before the job's next answer |

### 5.3 Hop chains (effect 14 `hops`, event 18 `hopped`, step `order`)

Status: the kinds and the layout are in the generated tables (abi_id changed). The core does not emit `hops` yet; the rules below bind the core's use. PROTOCOL.md "Proposal: hop chains" has the host rules and the worked examples.

Pi's Session line is a FIFO of jobs, and commit order is the order of `#enqueue` calls. Host-run code and Pi's scheduler enqueue from one microtask queue, and ties inside a round are decided by FIFO position (a host delivery and the core's `#runAbort` `#step` share round 2064933 in E23, host first; no static tie rule exists). The core therefore keeps no clock. It asks the host to spend real microtask hops, so one shared FIFO orders host and core.

| kind | name | payload | meaning |
|---|---|---|---|
| effect 14 | `hops` | `u32 n` (`n >= 1`), `u32 at`, `commit*` | a chain of `n` microtask hops; the effect ID names it. The host queues the first hop when it processes the effect, each hop queues the next, and after the nth it sends `hopped`. A chain that carries commits has `0 < at < n`: after hop `at` the host applies them in order, each in its own transaction, in the microtask of that hop, where a step of a `hopped` event at that hop would have carried them (the core parks a line job's statements for its COMMIT hop this way). Otherwise `at` is 0 and no commit follows. Each `commit` is encoded as in a step (section 5) |
| event 18 | `hopped` | `u32 effectId` | the chain ran its `n` hops; the core runs the work parked on it. `cancel` (effect 5) drops an open chain |

Step layout: `order := u16 n, (u8 region, u16 index)*` follows the effects (`region` 0 = notice, 1 = effect; `index` into that region). `n` is 0 or `nNotices + nEffects`. The builder emits a full list when the step has both notices and effects, in the order the core called `Notice` and `Effect`, so core code must call them in Pi's synchronous order; the host processes the step in that order. With `n = 0` the host applies all notices, then all effects. Without `order`, a chain started after an `api_result` in Pi would be queued before the host resolves it.

Rules. (1) The core parks each await chain of Pi that stands between two Session-line-visible points (an enqueue, a publication, an effect start) as one `hops` effect, with `n` counted from Pi's code, and runs the parked work on `hopped`. Each chain starts at its true origin: commit listeners run synchronously at adopt (`session.ts:438-443`), before the job's own resolution. Each `n` cites the awaits it models (the continuation after storage resolves, `session.ts:428`: caller of `commitWith` +2, async caller +3; reserve to `#step`, `scheduler.ts:746`, `:899`, `:919`, `:1001`, +3; tool `execute` +4 and settle +5). None is fitted to a row. (2) Several chains may be open. (3) A host event arrives at its real queue position and the core handles it at once, with no stamp. The host sends `abort_done`, `phase_done`, `tool_done` and `hook_done` in the microtask where Pi's await of that handler resumes, with no extra host hops, and the core enqueues its next line job synchronously on receipt (E23: handler returns, `#step` `commitWith` in the same microtask, `scheduler.ts:982`, `:987`, `:1001`). (4) State a host read can observe (snapshot cache hits, the scheduler's live set) changes at Pi's adopt point, after the storage hops, not at job start. (5) The core emits `hops` only while the host can interleave: a host handler, a transaction or a call is in flight, or a host-facing `api` answer is pending. With none of these it drains its own queue as before, so an idle core, an idle host and a headless run cost nothing. A step that parks nothing emits no `hops`. (6) Storage time is part of the model: a commit is charged the hops of `SqliteStorage.commit` (`src/storage/sqlite/storage.ts:174-197` and its async helpers) over the gate's serial `piDurableDatabase`, derived await for await, with the measured per-statement offsets as the oracle (dcore-trace: task-only commit 15, entry+submission+task+document 32, +2 behind the gate's lazy MemoryStorage proxy). Reads cost hops the same way (readContext 15-30).

### 5.4 Origins: the async context of the code that runs

Pi programs observe `AsyncLocalStorage`: a store set around `Conversation.submit` reaches the model call of the turn it starts, and a turn that `Harness.open` resumes runs in `open`'s context whatever call comes later. Each continuation in Pi runs in the context that queued it: a microtask in the context of the code that called `queueMicrotask`, a Session line job in its enqueuer's (`session.ts #enqueue`, `:881`, `this.#tail.then(job)`), an `await` in its function's. The scheduler queues its drain with `queueMicrotask` from `#kick` (`scheduler.ts:1404-1411`), so an idle scheduler runs the tasks it starts in the context of the code that kicked it (a commit listener runs in the committing job's context, `session.ts:765`, and the job runs in its enqueuer's, `:881`, so a turn runs in the context `submit` was called in), and a scheduler that is already draining keeps the context of the kick that queued its pass: `#drain` loops while `#dirty` (`:1413-1426`) and `#start` runs the task's invocation inside it (`:1656-1668`), whose `finally` kicks again in the invocation's context. An input a tool submits in another context therefore reaches its turn in the turn's.

The ABI carries this as an **origin**, a `u32` the host assigns to the async context (an `AsyncLocalStorage.snapshot()`) of one host call; 0 is none. The host snapshots the context of each call a client makes (`open`, `submit`, `abort`, `registry`, `api`, `inspect`, `resume`, `seal`, `close`, `tx`, `timer`), numbers the snapshot when it delivers the event, and passes the number as the `origin` argument of `step`. The core keeps one current origin, the context of the code that is running:

- an event a host caller makes runs in its `origin`; the completion of an effect (`model_event`, `model_bytes`, `tool_progress`, `tool_done`, `hook_done`, `phase_done`) runs in the origin the effect was started in, and `hopped` in the origin of the work parked on the chain, whatever context the host made the call in (an `await` resumes in its function's context whoever settles the promise); the `origin` argument of those events is ignored and the host passes 0;
- the Loop captures the current origin when it queues a microtask or a Session line job and makes it current again when it runs it, so `kick` queues its drain in the kicker's origin, and a kick that finds the scheduler draining queues nothing;
- every notice and effect leaves stamped with the current origin (`notice := u8 kind, u32 origin, ...`, `effect := ..., u8 kind, u32 origin, ...`). The host runs the effect's handler, and the timer set for a `timer` effect, and the listeners of the notice (commit listeners, `onReport`, `onWake`, watches) inside the snapshot of that origin. A `hops` effect has origin 0: it runs no host code, and the core keeps the origin of the work parked on it. A core with no origin on any event stamps 0 and a host runs 0 in no snapshot of its own (the native host passes 0 everywhere).

The host keeps a snapshot until the core says it holds no work for it. The core counts what holds an origin: an event from its arrival to the end of its step (and while it waits for rows), a queued line job, a started effect until its completion, a hops chain until its `hopped`, and the microtasks a parked Loop keeps across steps. An origin whose count is zero at the end of a step without reads goes to the host in that step's `origin_free` notice (kind 10, last in the step's order); a step that is rejected or fatal carries none, and the host drops the origin of the event that produced it. A reopened core (section 4.6) resumes in the context of the host's first `open`, and a request fed again keeps its caller's.

## 6. Effects

| kind | name | payload | host action |
|---|---|---|---|
| 1 | `timer` | `u32 timerId`, `f64 at`, `u8 class` | `class` 0, volatile (a throttle window): `setTimeout`, and a host may lose it; 1, durable (a sleeping task's wake): keep the object alarm at the earliest durable `at`, and the core re-derives the timer from the task's checkpoint on reopen; 2, background (an idle context or document expiring, memory only): a host that cannot keep a pending timer without keeping its process alive may decline it, and the core frees that memory at later commits instead |
| 2 | `timer_clear` | `u32 timerId` | |
| 3 | `model_context` | A body recipe (§10.2) whose expansion is the JSON `{model:{provider,modelId}, conversationId, context, options}`. The core builds the framing as text segments and names the model's messages as one range segment: the committed context of the conversation, cut off at the entry `tail` under the head marker entry `head`. The host derives the messages from the entries in storage (the `entries` table, `CAST(record AS BLOB)`) with pi-durable's range algorithm (`src/harness/context.ts`) before it parses the JSON, so a model request neither copies the transcript through the core nor requires the core to hold it. `context` is exactly the pi-ai `Context` pi-durable 1.0.4 passes, `{messages}` (`src/harness/generation.ts:402`): the system prompt and tool declarations are positional `pi.system` messages inside `messages` (§7.4), not separate fields. A request whose messages a `beforeRequest` hook replaced, or whose conversation or entry ids exceed `u32`, carries them as text instead. `options` is the request's `SimpleStreamOptions` without `signal` (`reasoning`, the provider `sessionId`, the pinned stream options). A host keeps the range it derived for a conversation, so a later request reads only the entries after it, and drops it when the conversation unloads or has been idle for `contextRetentionMs` | run pi-ai `streamSimple` (or the scripted model) on the derived message list; send `model_event` |
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
| 14 | `hops` | `u32 n` (`n >= 1`), `u32 at`, `commit*` | a chain of `n` microtask hops, with the commits it applies after hop `at` (section 5.3); the effect ID names it; the host sends `hopped` after the nth |

## 7. Notices

| kind | name | payload |
|---|---|---|
| 1 | `published` | JSON: committed document ops by document, appended entry IDs, task status changes, submission settlements; drives `Submission.wait`, watches and Chord clients (§9) |
| 2 | `api_result` | `u32 requestId`, JSON |
| 3 | `tx_result` | `u32 txId`, JSON (`ready`, `waiting` for the line, an op's result, or `rejected` with Pi's error) |
| 4 | `report` | JSON for `HarnessOptions.onReport` |
| 5 | `sidecar` | JSON `{conversationId, rows}`: the core wrote a sidecar chunk in this step's commits (diagnostics only) |
| 6 | `progress_ack` | `u32 waitId`, `u8 outcome` (0 committed, 1 rejected), JSON error when rejected: settles the promise of a tool's `api.details()` |
| 7 | `doc_cache` | JSON `{cached, idle, attached, sweeps}`: the core's document cache sizes, published after a step in which they changed; the host's synchronous `documentCacheSizes` replicates them (`attached` counts the incarnations a document watch pins) |
| 8 | `index_sizes` | JSON `{live, settled, edges, below, roots, unloaded, dropQueue}`: a `harness`-mode core's scheduler ownership index sizes, published after a step in which they changed; the host's synchronous `SCHEDULER_INDEX_SIZES` getter replicates them |
| 9 | `wake` | JSON `{active, at?}`: the `Harness.wake()` value of a `harness`-mode core whose `open` payload sets `onWake`, published after each recomputation (`HarnessOptions.onWake`); the host delivers a changed value to its callback, and again after a throw, so it keeps the last value it delivered. With `onWake` the core arms no timer for sleeping tasks: the host arms its own alarm from `at` |
| 10 | `origin_free` | `u32 n`, then `n` × `u32 origin` | the origins the core holds no work for any more (section 5.4): the host drops their async contexts. The host consumes it; it reaches no listener |

Sidecar writes, including the `CREATE TABLE IF NOT EXISTS` on first use, are ordinary commits on `pig_*` statements outside Pi's `next_seq` sequence; their `seq` is `-1` (ADR D6).

## 8. Extension transaction channel

`runtime.commit(tx => ...)`, `session.commit(...)` and other host-side commits use one protocol:

1. `tx {op: "begin", invocation?, conversationId?}` → `tx_result ready` with a `txId`, or `waiting` when the Session line is held; the host awaits the `ready` notice that a later step delivers.
2. `tx {op: ..., ...}` for each `Tx` operation (`conversation`, `doc`, `appendEntry`, `createTask`, `placeSubmission`, `settleSubmission`, `entry`, ...). Document mutations travel as Chord `Op[]` produced by the host's draft proxy. The core enforces `ReadAfterWrite` and every other §4 rule and answers each op with `tx_result`.
3. `tx {op: "end", state?}` with the callback's returned next task state, or `{op: "abort", error}` → a step carrying the commit, or `rejected`.

The line stays held from `begin` to `end`, across host awaits, as §4 requires; events for other work that arrive meanwhile are queued inside the core and processed after `end`. This plane is JSON because extension commits are not on the hot path; built-in tasks never use it.

A host that stops waiting for `ready` (its caller's context ended while `begin` was `waiting`) sends `{op: "abort"}` for that `txId`. The begin stays queued behind the line, so the core marks it abandoned: it keeps its turn in the line's order, never answers `ready` and gives the line up when that turn comes, so a transaction nobody ends cannot hold the line and block every later job and the close. An `abort` of a transaction that already holds the line rolls it back as above.

Host code reaches the core only through these channels. Every method of the facades Pi hands to host code (`TaskRuntime`, `src/types.ts:170`; `ToolExecutionApi`, `src/harness/types.ts:166-204`; `HookApi`, `src/harness/types.ts:605-610`) maps to one of them:

| method | channel |
|---|---|
| `commit(tx => ...)` | `tx` (section 8), bound to the calling task |
| `memo(name, candidate)` | `api` request; the core commits it as one gated commit (§5.1) and answers with `api_result` after the commit is applied |
| `memo(name)`, `snapshot`, `snapshotAsOf`, `getTask`, `outcomes`, `ownedTasks`, `entry`, `context`, `conversation`, `agent()` | `api` request answered by `api_result` from committed state |
| `waitForTask`, `watchDoc` and other observations | `api` request; the core answers when the awaited state is published, and `published` notices drive watches |
| `createTask` | `tx` with a single `createTask` op; the host computes the definition's `initial(input)` before sending it |
| `sleepUntil(until, checkpoint)` | `api` request; the core commits the task's sleeping state (`checkpoint` is its next state) and answers once it is committed; the task then holds no invocation, and its wake is a durable `timer` (class 1), or a `wake` notice when the host took `onWake` |
| `Harness.wake()`, `Harness.retryTask(taskId)` | `api` request answered by `api_result` (`wake`: at once; `retryTask`: once the parked task's restart count is cleared and scheduling is enabled) |
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
| `snapshotAsOf` | doc, at | the document state at entry `at` (Pi's `snapshotAsOf(doc, at)`) or null | at once |
| `watch.open` | doc, view (`{kind:"view", conversationId}`) or graph target, watchId | the first frame | at once; later frames arrive as `published` notices carrying watchId |
| `watch.close` | watchId | null | at once |
| `getTask` | taskId | the task record or null | at once |
| `waitForTask` | taskId | the settled receipt | when the task is terminal and published |
| `waitForIdle` | conversationId (optional: every ownerless conversation) | null | in the same step when the scope has no live non-background task; else when the scheduler settles the scope idle; `Harness is closed` once the Harness closed |
| `submission` | submissionId, wait (optional: the read is Submissions.wait's, not status's) | the submission record, or null | a Session line job that reads the record once |
| `scanEntries` | query, limit, cursor | the page of entries | a Session line job that reads one page (Conversation.entries) |
| `outcomes` | taskIds | the outcomes in order, or an error naming the missing one | at once |
| `ownedTasks` | by (the invoking task) | copies of the live tasks it owns directly, in ID order; an error once its invocation has ended | at once |
| `entry` | conversationId, entryId | the entry record or null | at once |
| `context` | conversationId, at | the ContextView `{head?, entries, contributions, messages}`, or `{"$context":{conversationId, head, tail}}` for a conversation with a visible entry: the core does not build a long context, it names it (the same bounds a `model_context` range names, §10.2) and the host that serves the read derives the ContextView from storage with pi-durable's range algorithm and resolves the request with it. A conversation without a visible entry answers the empty view | at once |
| `conversation` | conversationId | the conversation handle data or null | at once |
| `agent` | conversationId | the resolved agent | at once |
| `sleepUntil` | taskId, until, checkpoint | null | once the sleep is committed (the task's sleeping state, `checkpoint` its next state); the task then holds no invocation |
| `wake` | none | `{active, at?}`, the Harness's wake | at once; a `harness`-mode core only |
| `retryTask` | taskId | null | once a parked task's restart count is cleared and scheduling is enabled; an error for an unknown task |
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

### 10.1 Records the core does not hold

The core keeps the index of a conversation's entries and holds the record text of its newest entries only. It reads any other record text from storage through `pig.read` (§4.2), and a host reads the records a recipe names, and derives the contexts its ranges name, from the same storage. No import or export exists for this: there is no host-held arena, `hist.copy` or `hist.drop`, and no capability flag; every host expands a `model_context` recipe.

### 10.2 Body recipe (the `model_context` effect payload)

```text
recipe  := u32 nSegments, segment*
segment := u8 0, u32 len, bytes                         text the core built
         | u8 1, u32 conv, u32 rec, u32 off, u32 len     bytes [off, off+len) of the record of entry rec of conversation conv
         | u8 2, u32 conv, u32 head, u32 tail            the model messages of a committed context
```

`rec` is an entry id and `off` is relative to the start of the stored record text (its bytes as `CAST(record AS BLOB)` returns them). The expansion is the concatenation of the segments, exactly the JSON text of the payload. A reference names a record the host's storage has when the effect is dispatched: the host has applied every commit of the step before it dispatches the step's effects. A host reads consecutive references to ascending entries of one conversation as one range. A recipe that is truncated, or names a record or byte range storage does not have, is a fatal host error. A range segment stands for the comma-separated JSON of the model messages that spec section 2.1 derives for conversation `conv` from the entries visible to it (its own, and its fork ancestors' up to each fork point) in `[head, tail]`: `tail` is the entry the context is cut off at and `head` is the entry of the context's head marker, 0 when it has none. The core writes one range segment, as the whole of `context.messages`. A host derives the messages and fails on a `head` that is not a head marker visible to `conv`, a `tail` that is not visible to it, or a conversation it does not have; the failure is a fatal host error. A host may keep the derived range and extend it with the entries after its tail while the head marker is the same. An id that exceeds `u32` is not named by a segment: the core writes the messages as text. Only effect 3 carries a recipe.

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
