# ADR-0001: Durable core architecture

Status: proposed, 2026-10-06. Deciders: owner, lead. Scope: the Pi Durable runtime that PiG runs inside a Cloudflare Durable Object, natively in PiG, under Pi Pocket-style Node hosts, and in any other host with a synchronous SQLite.

Each decision states its evidence or the hypothesis that the bake-off ([BAKEOFF.md](BAKEOFF.md)) tests. "E" marks a measured or documented fact with its source; "H" marks a hypothesis with its test. Sandbox numbers come from the researcher's 1-vCPU box and are ratios only until rerun on smc1.

## Context

pi-durable 1.0.4 is correct and portable but treats storage as asynchronous and re-derives context from storage. Per measured eight-tool turn it issues 1,019 SQL statements and reads 1.4 MB of JSON at 50 turns of history, and 2,639 statements, ~212,700 rows and 81 MB at 3,500 turns, to write 36 KB (E: spike `pix.ts`, reproduced to the statement by the durable-perf and wasm-spike lanes). Writes (372 statements in 77 commits) and open (10 statements) are already flat. Upstream has an unreleased fix that reuses the scanned range within one task invocation; warm turns then stay proportional to history (research notes 1).

tardigrade 0.44.0 keeps an event log plus a full-state JSON checkpoint every 500 events, digest-checked and schema-decoded on open; its cold open is the slowest of the three (smc1 Miniflare cold 265/518/1,401/3,513 ms at 50/250/1,000/3,500; bench-pig handoff).

The researcher's ~1,400-line Go reference core, scoped to the durable-bench workload, measured warm 14/13/12/14 ms and cold 94/78/136/269 ms at 50/250/1,000/3,500, with pi-durable at 125/164/357/966 warm and 191/254/529/1,220 cold on the same box, row-identical stores and matching fingerprints (research notes 2). The empty-target harness floor is ~8 ms, and the turn's SQL alone replays in 3-5 ms, so warm is at the floor. The open prizes are cold open, module size and memory per conversation (research notes 3).

Durable Object facts used below (E: Cloudflare docs fetched 2026-10-06 and re-fetched by the design review the same day: `durable-objects/platform/limits` (updated 2026-06-01), `durable-objects/platform/pricing` (2026-09-30), `durable-objects/api/sqlite-storage-api` (2026-09-21), `durable-objects/api/alarms` (2026-04-21), `durable-objects/concepts/durable-object-lifecycle` (2026-09-30), `workers/platform/limits`, and `d1/platform/pricing` for the rows-read definition the DO pricing page defers to):

| fact | value |
|---|---|
| SQL API | `ctx.storage.sql.exec()` is synchronous and in-process; `transactionSync(fn)`; cursors expose `rowsRead`, `rowsWritten` |
| write coalescing and output gate | writes with no intervening `await` commit atomically; outgoing network messages are held until prior writes are durable |
| row limits | string, BLOB or row ≤ 2 MB; SQL statement ≤ 100 KB; ≤ 100 bound parameters; 10 GB per object (Workers Paid) |
| CPU and wall time | 30 s of active CPU per invocation by default, configurable to 5 min (`limits.cpu_ms`), reset by each incoming request; an alarm handler has a 15-minute wall-time limit; HTTP/RPC wall time is unlimited while the caller stays connected |
| billing (Workers Paid) | rows read $0.001/M after 25 B/month, counted as rows scanned (an `UPDATE` or `DELETE` reads the rows it matches); rows written $1.00/M after 50 M/month, every index row update counts as an additional row, deletes count as rows written, each `setAlarm()` is one row written; stored data $0.20/GB-month after 5 GB-month; requests $0.15/M after 1 M/month, alarm invocations included; duration $12.50/M GB-s after 400,000 GB-s, wall-clock at 128 MB per object while active or idle-but-not-hibernatable, even when objects share an isolate; an idle object that is eligible for hibernation is not billed for duration |
| memory | 128 MB per isolate including Wasm; objects of one class may share an isolate and its 128 MB; an isolate that exceeds 128 MB finishes its in-flight requests and is replaced by a new isolate |
| lifecycle | hibernatable idle objects hibernate after 10 s; a pending `setTimeout`/`setInterval`, unfinished I/O or an open outbound connection prevents hibernation; non-hibernatable idle objects are evicted after 70-140 s; each pending operation prevents eviction for at most 15 min from its start; deploys and runtime updates restart objects, and a request that touches storage during shutdown is stopped |
| alarms | one alarm per object; at-least-once; on exception, up to 6 retries with exponential backoff from 2 s; `deleteAll()` deletes the alarm from compatibility date 2026-02-24 |
| Worker size and startup | 64 MiB uncompressed, no compressed limit (the 10 MB compressed figure in research notes 1 is outdated); global scope must start within 1 s |

## Decisions

### D1. Synchronous core: event in, step out

The core is a deterministic state machine with no I/O, no goroutines, no timers and no clock; its only input besides events is the provider document's `uuidv7()` value, which it pulls from the host (`pig.uuidv7`, ABI section 4.2), because pi-ai keeps that generator's state per process; capture runs serve the recorded values. WASI `random_get` is left to the toolchain runtime. A host feeds it one event (with the Harness clock value `now`) and receives one step: an ordered list of commits, reads, notices and effects ([ABI.md](ABI.md) section 3). The host executes commits, answers reads, publishes notices, then starts effects. Effect results return as later events.

E: every target host has synchronous SQLite in-process: DO `sql.exec`/`transactionSync`; `node:sqlite` `DatabaseSync`, which Pi Pocket uses through `openNodeSqliteStorage` (lead's answer in research notes 3; verify in the pi-pocket source); modernc SQLite in PiG. The reference core's warm turn is flat at 12-14 ms against an ~8 ms harness floor. A JS→Wasm call costs ~8-10 ns (spike; durable-perf measured 7.9 ns).
Rejected: JSPI or stack switching to keep an async core (a suspended stack is not durable, and pi-durable's task checkpoints already are the state machine); an async core with a storage interface (pi-durable's model, the source of the re-read cost).

### D2. The core writes Pi's store, row for row

The core emits the exact row writes pi-durable 1.0.4 makes, commit for commit, under [CONTRACT.md](CONTRACT.md). The core owns the SQL text (ABI `sql_table`), so every host runs the same statements. Statements are not required to match Pi's: the core omits reads it does not need and `record_ids` claims for IDs it knows.

E: the reference core and the wasm-spike core are row-identical with pi-durable at all four sizes, and handoff in every order ends equal to the pi-only control (research notes 2; wasm-spike update 3).
Consequence: a pi-durable process can open any store the core wrote, and the reverse. No migration exists and none is needed.

### D3. Pi's commit granularity is fixed

77 commits per eight-tool turn stay 77 commits. `next_seq` values are stored in `entries.commit_seq` and `document_revisions.seq`, so a different grouping is a different store (CONTRACT section 3.1).
E: collapsing a turn's 77 commits into one saved 1-2 ms (research notes 1). In a DO the 77 `transactionSync` calls of one step are coalesced by the platform into one durable write before the next effect anyway.

### D4. Bytes and a fixed-width index, not objects

Each loaded conversation keeps its entry records as the stored UTF-8 bytes in an append-only arena and a fixed-width index (per entry: id, offset, length, commit seq, kind code, head/edit/model flags, first-message role, stop-reason code; per model message: role and offset). The warm path never decodes a record. Context derivation (§2.1) runs over the index and maintains its result incrementally: append-only extension on new entries, full re-derivation only when a delta adds a head marker or an edit, or when a read is cut off inside an edited range. That is the durable-perf lane's `contextCache` design, which is covered by differential tests against full derivation (durable-perf lane, commit 164937976).

New records are encoded by a JSON.stringify-faithful encoder; stored records are read by a structural byte scanner (a real tokenizer that skips values, tolerant of unknown fields and key order). Neither uses `encoding/json`.

E: at 3,500 turns (4.5 MB, 11,669 entries) Go `encoding/json` decodes in 115-256 ms in Wasm against V8's 38-43 ms; raw rows plus offset and role index take 27 ms; the wasm-spike byte scanner takes 34 ms against 165 ms for its decoder and matches a JS oracle on every fixture; Go `encoding/json` encodes at 29-40 MB/s against V8's ~600 MB/s; PiG's current entry codec is 17x slower than plain structs natively (spike; wasm-spike update 3; durable-perf).
H-D4: the index costs ≤ 32 bytes per entry, ≤ 0.5 MB at 3,500 turns. Test: memory per conversation, BAKEOFF M5.

### D5. Cold open reads O(live state + active context), not O(history)

Open reads, in order: `durable_metadata` (1 row); the conversation records it needs; live documents (`pi.live`, `pi.inbox`, `pi.usage`, agent, provider) by address, base plus delta tail; live tasks and submissions by status index; then, lazily per conversation at its first use, the transcript. The newest head marker `H` is found with the `entry_heads_by_conversation` partial index (1 row), and the transcript load starts at the entry `H.head` names (the first kept entry, older than `H` itself), because context derivation never reads before it (`src/harness/context.ts:22-39,96-118`). Fork ancestry loads the parent range through `parent.at` the same way. Terminal tasks and old history are never loaded; the host serves them from SQL on demand (CONTRACT section 3.10).

E: Pi's own open is 10 statements at every size (spike). Compaction places head markers, so in a real conversation the active range is bounded by the model's context window. durable-bench disables compaction and sets a 10⁹-token window, which makes its active range the whole history: the benchmark is the worst case for this decision, not the typical one.
H-D5: with compaction on (W4), cold open at 1,000+ turns is flat in history length. Test: BAKEOFF W4a.

### D6. Sidecar cache, validated cheaply, never authoritative

A `pig_`-prefixed sidecar holds, per conversation, the index (default) or the index plus concatenated record bytes ("snapshot") for a prefix of its entries, in chunks of at most 1 MiB (under the 2 MB row limit).

Validation, in order:

1. Fast path: the sidecar header's `next_seq` equals `durable_metadata.next_seq` (already read by D5) and its `core_id` equals the running core's layout identity. Zero extra reads. This is the researcher's rule.
2. Prefix path: otherwise, if `core_id` matches, read the last covered entry by primary key (1 row) and compare id, `commit_seq`, length and a 64-bit hash of its bytes. If equal, the prefix is still valid, because entries are immutable and appended in commit order under one Session line, and load the delta `WHERE conversation_id = ? AND id > last` (rows = new entries only, through `entries_by_conversation`). This keeps the sidecar useful when another conversation of the same Session (a sub-agent), a commit after the last idle boundary (a crash before the sidecar write), or a pi-durable process advanced `next_seq`. The last-entry comparison is defence in depth against a store edited outside the Session contract; it is not needed for point-in-time recovery, because PITR restores the `pig_` tables together with Pi's.
3. Otherwise ignore the sidecar and load from rows.

The sidecar is written at idle boundaries (a run settles, or the step that leaves no live task), as one appended chunk row per conversation, with chunks merged once 32 accumulate. It is never written inside a Pi commit and never read by Pi.

E: rows-to-index 27 ms vs snapshot load 10-12 ms at 3,500 turns, and a full snapshot costs about half the database again in storage (spike). pi-durable's storage only checks `durable_schema.version` and ignores unknown tables today (research notes 1; confirmed in review: no `sqlite_master` read anywhere in `packages/durable/src`, and `applySqliteMigrations` touches only `durable_schema` and its own schema, `src/storage/sqlite/migrations.ts:95-125`; DO stores also carry workerd's `_cf_*` tables).
H-D6a: index-only plus lazy bytes beats snapshot on cold for real (context-plane) workloads once D5 bounds the range; snapshot wins only on unbounded ranges. Test: BAKEOFF H-cold.
E-D6b (was H-D6b, settled by source reading in the design review): entry IDs are monotone in commit order, so for each conversation every entry committed after a sidecar point has an ID above every covered entry. Evidence in pi-durable 1.0.4: spec §2.1 states "Entry IDs are Session-global and ordered"; every `mintId` call runs inside a commit callback on the Session line (`session/transaction.ts:305,362,405,431,609`, and fork document copies at `session/forks.ts:76`, reached from `transaction.ts:322` inside `#stageConversation`); the line runs one job at a time (`SessionImpl.#enqueue`, `session/session.ts:529-536`); an entry is written in the commit whose callback minted its ID (`#appendEntry`, `transaction.ts:358-379`); `SqliteStorage.mintId` increments an in-memory counter (`storage/sqlite/storage.ts:187-191`) that starts at the stored `next_id` (`:151`), and every commit stores `max(stored next_id, minted counter, max written id + 1)` (`:165,176-179`), so a later process starts above every written ID. A rolled-back callback leaves a gap, never an inversion. The core must keep the same rule, which the contract already requires. A property test (concurrent tasks, forks, rolled-back callbacks, a pi-durable turn between core turns) stays as a regression guard in A4; a `commit_seq` bound in step 2 is not needed.
Policy: the sidecar has one unversioned shape; `core_id` is a content identity, so a changed layout is an invalid cache that rebuilds, with no reader for older shapes (AGENTS.md, Pig-owned format policy). The prefix is to be agreed upstream (RISKS-AND-QUESTIONS Q2).

### D7. Commit before effect, and a liveness alarm

A step's commits are applied before any of its effects starts. In a DO the output gate also holds outbound `fetch` until those writes are durable, so the §5.2 effect sandwich (intent committed, effect, outcome committed) holds physically. The host sets a watchdog alarm when the Session becomes busy (first effect while no alarm is set) and clears it when nothing is live, so an object killed mid-turn by a deploy or a runtime update, or evicted after a pending operation outlived its 15-minute eviction guard, wakes up, reopens, reconciles and resumes without waiting for a user request. One `setAlarm()` is one row written, so the host calls it only when the target time changes.
E: DO output gate, shutdown and alarm semantics above. Pi on a server relies on its process restarting; a DO has no process to restart.

### D8. Time and timers are explicit; alarms are wake-at-T effects

Every event carries `now`. The core never reads a clock. Waiting (`sleep(until)`, retry backoff, deferred polls, the partial-commit throttle) is a `timer` effect with an absolute time and a flag: durable timers (`sleep`, `retry`, `poll`) are re-derived from checkpoints on reopen; volatile timers (throttle) may be lost, which loses only the uncommitted throttle window, as §8 allows. A DO host keeps one alarm at the earliest durable timer (and the watchdog), uses `setTimeout` for volatile timers, and treats a duplicate or early wake as a no-op because the core re-checks due times. A pending `setTimeout` prevents hibernation and keeps duration billing running, so volatile timers exist only while a stream or tool runs, which already holds the object, and the host clears them on `timer_clear`.
E: alarms are at-least-once (docs); tool output's `Progress` derives its next commit time from the write's start time and size (`output.ts:310-331`), while generation partials re-arm a fixed `partialIntervalMs` timer after each commit settles (`generation.ts:372-414`; CONTRACT section 3.9). Both are volatile timers.

### D9. Hibernation is a cache drop

Everything in the core is a cache of SQLite, except what Pi also loses on a process restart: the faux provider's prompt cache, unflushed throttle windows, hook and phase continuations of an in-flight invocation (Pi reruns the phase from its checkpoint), and live provider streams. The core may be dropped at any idle point; the price is one cold open. In-flight provider streams keep the object non-hibernatable, which is correct and billed as duration either way.
E: lifecycle docs above; §5.1 reconciliation.

### D10. Memory is budgeted per conversation

Unit of account: bytes resident per loaded conversation (index + arena + derived context + live documents) plus the per-instance floor. Conversations unload when idle (their state is a cache). Three residency policies are bake-off hypotheses: core-held bytes (L1), host-held bytes with a core-held index (L3), and re-read per model call from sidecar chunks (variant of L1).

Wasm linear memory does not shrink, so a host recycles an instance whose memory exceeds a high-water mark after its conversations unload; recycling costs one instantiate plus cold opens. One instance per isolate serving many objects (the ABI's session handles) replaces N per-instance floors with one, at the cost of a shared failure domain: a trap discards the instance and every object on it reopens.
E: Go Wasm initial memory 3.5 MB; steady 9 MB with snapshot and indexes at 3,500 turns; the reference core used 13 MB at 3,500 (spike; research notes 3). Isolates share 128 MB across objects (docs). Duration is billed at 128 MB per object whatever it uses (pricing footnote 5), so memory per object does not change the bill; it decides how many objects fit in one isolate before the runtime replaces it, which costs every object on it a cold open.
H-D10: shared instance per isolate lowers memory per object below 1 MB at 50 turns without hurting cold. Test: BAKEOFF M5 with K = 1, 8, 32 objects per isolate.

### D11. Provider bodies are spliced; provider streams are parsed where the bytes are

Two model planes, both in the ABI:

- Context plane: the core emits the pi-ai `Context` as JSON spliced from stored message bytes (system prompt from sections, `messages` array copied record by record, tools). The host runs pi-ai (JS) or the scripted model and feeds back assistant events. This is what pi-durable does, minus re-reading and re-parsing.
- Wire plane: the core builds the provider HTTP body with PiG's Go provider conversion (`ai/`), caching each message's encoded fragment per (api, model) and re-encoding only the position-dependent tail (cache markers, last-turn transforms); the host only does transport and authentication and feeds back raw response bytes, which the core parses.

E: splicing equals V8 `JSON.parse` plus `JSON.stringify` output and is 20x faster than Go re-marshal at 3,500 turns (wasm-spike update 3).
H-D11: the wire plane beats the context plane on cold and warm for real providers because the host never parses the context. Risk: Wasm size from provider code, mitigated by build tags per provider family. Test: BAKEOFF W4f with recorded OpenAI Responses and Anthropic Messages streams. Until it passes, the context plane is the default and the only plane the bake-off requires.

### D12. Extension code runs in the host

Hooks, tool `execute`, `HarnessOptions.env`, custom task phase and abort handlers, and extension definitions are host code (JS in a DO, Go or subprocess extensions in PiG). The core suspends a built-in task at a hook as an effect and resumes it on the result event. Custom task handlers run as `phase` effects; their `runtime.commit(tx => ...)` callbacks use the ABI's transaction channel, which holds the Session line for the duration of the callback, as Pi does (§4). The core owns the Session line, the scheduler, the built-in tasks (generation, tool, compaction), context derivation, documents and the store format.
Consequence: a JS host exposes pi-durable's public API (Harness, defineTask, defineExtension, ...) over the core, so the upstream examples run unmodified against it (CONTRACT corpus E00-E31). In PiG, `durable/harness` keeps its public API and becomes a driver over the core once the core passes the contract.

### D13. Billing: rows written equal to Pi by construction, rows read independent of history

Rows written per turn are Pi's (D2) plus the sidecar (≤ 1 row per loaded conversation per idle boundary, plus an occasional merge, plus its index rows if it has any) plus alarms (≤ 2 per busy period). The core issues no `SELECT` on the warm path: it holds `next_seq` and folds a single-writer guard into the metadata write (`UPDATE durable_metadata ... WHERE singleton = 1 AND next_seq = ?`, `rowsWritten == 1`, else fatal). Warm rows read are therefore only the rows Pi's write statements themselves scan: the conflicting row of each task and submission upsert, the metadata row, and the revisions each `DELETE FROM document_revisions` removes. That is a few hundred rows per turn at every history size, not zero (durable-perf measured ~540 per warm turn at 3,500 for PiG's native port, which still issues some reads). Cold rows read are D5's.
E: pi-durable reads ~212,700 rows per turn at 3,500 turns (research notes 3; durable-perf counted 213k). At the documented rates, Pi's reads cost about $0.0002 per turn. Rows written per turn, counted from the schema (`migrations.ts`): 36 new `record_ids` rows (ignored claims write none), 77 metadata rows, tasks with five secondary indexes (17 inserts at 6 rows; 67 updates at 1 row plus each index whose column the upsert changes), 52 revision inserts at 3 rows (table, the primary-key autoindex, `document_revisions_by_kind`), 35 deletes at 3 rows per removed revision, 18 entries at 3 rows, and the submission rows: roughly 600-950 rows, about $0.0006-0.001 per turn (measure, BAKEOFF M6). Writes, not reads, dominate the storage bill, and the core cannot reduce them without breaking D2. Duration billing is wall-clock and dominated by model latency; CPU savings shorten it only by the saved milliseconds.

### D14. Limits are honoured, not worked around

A record above 2 MB fails exactly as it fails for pi-durable on the same platform. Sidecar chunks stay ≤ 1 MiB. Parameters are always bound (statement text stays far below 100 KB; Pi's widest write, the `documents` insert, binds 9 parameters, `src/storage/sqlite/storage.ts:817-829`). The core holds no single allocation proportional to history except the arena, and the arena is optional (D10).

### D15. One core, several bindings

The core is one Go package (proposed `durable/core`): no goroutines, no I/O, no reflection and no `encoding/json` on hot paths, no package-level mutable state outside session handles. Bindings: a WASI reactor (`GOOS=wasip1 -buildmode=c-shared`, `go:wasmexport`) and native Go (a direct call API; ABI section 11). TinyGo is a second compiler for the same source, kept viable by the no-reflection rule. The TS control lane re-implements the logical ABI for the bake-off only; it is a measurement control, not a second product core.
E: Go 1.27.1 wasip1 reactor works; 4.65 MB raw, 1.27 MB gzip with the spike core; `GOOS=js` is similar in size but costs ~13 µs per call; TinyGo 0.42 builds the spike core at 1.0 MB raw, 0.37 MB gzip, but its `encoding/json` mis-decodes `json.RawMessage`, which the byte scanner does not use (wasm-spike update 3).

### D16. Failure model

A storage error the host cannot classify as a guaranteed rollback is fatal to the Session (§1 invariant 8): the host discards the session handle and the DO resets. A Wasm trap discards the instance. A rejected event (an API misuse Pi would reject) returns a rejected step with Pi's error and changes nothing. After any discard, the next event cold-opens from SQLite; nothing is lost that Pi would keep.

### D17. Native PiG uses the same core

Natively, one owner goroutine per Session holds the core and a modernc SQLite connection; effects run in owned goroutines with `context.Context` cancellation and post completions to the owner (AGENTS.md async rules). Parallelism across conversations and Sessions is Go's. The same binary can serve as the tool executor in a Cloudflare Container attached to a DO (research notes 1, item 6).

## Consequences

- The DO product, PiG native and a Node host share one implementation of Pi Durable semantics and one store format; the contract decides correctness for all of them.
- The bake-off chooses a language and memory layout for this design; it does not reopen D1-D9.
- The production core is a larger port than the bench slice: pi-durable's storage, session and harness are ~12,700 lines (research notes 3); PiG's current Go port is ~31,000 lines of non-test Go across `durable/` (including its env, tools and test-support packages). The existing `durable/` port remains the reference and fallback until the core-backed Harness passes the full corpus.
- New Pig-owned artefacts that users can see (the `pig_` tables, the DO host package) need an additive-feature record and a product-boundary classification before they land (RISKS-AND-QUESTIONS Q4).

## Alternatives considered

| alternative | why not |
|---|---|
| Keep pi-durable's async storage model and cache harder | that is the B2 baseline: it removes warm re-reads but still decodes every record into objects on cold open and keeps them as objects; it is the bar, not the design |
| tardigrade-style event log plus full JSON checkpoint | slowest cold open measured; a second store format breaks D2 |
| Collapse commits | breaks row identity for 1-2 ms |
| Component Model, WasmGC, memory64, multi-memory | extra glue copies or no Go/TinyGo/Rust/Zig target on the hot path (research notes 3, to verify) |
| Go SIMD for scanning | experimental and amd64-only; SIMD belongs to the Rust/Zig kernel lane if anywhere |
| Wasm pre-initialisation (Wizer-style snapshots) | deferred until the compile vs runtime-init split of cold is measured (BAKEOFF M1) |
