# Durable core: executable contract

Status: design, 2026-10-06. Owner lane: do-core-design. Consumers: every bake-off lane in [BAKEOFF.md](BAKEOFF.md), the production core described in [ADR-0001](ADR-0001-core-architecture.md), and its hosts ([ABI.md](ABI.md)).

The core is correct when pi-durable, run through the same scenario with the same inputs, writes the same rows at the same commit boundaries and sends the same model contexts. This document defines "same", lists the semantics that produce it, names the corpus, and specifies the capture and diff tooling that decides it. Prose here does not replace the spec; each rule cites it.

## 1. Pin

| item | value |
|---|---|
| reference | `@earendil-works/pi-durable` 1.0.4, with `@earendil-works/pi-ai` 1.0.4 and `@earendil-works/chord` 1.0.4 (the versions `durable/interop/package.json` and durable-bench already pin) |
| upstream commit | `7c10bd4337495ee613f2224843ecdf349b80d1df` (`internal/coding/pigversion`) |
| normative spec | `packages/durable/docs/spec.md`, sha256 prefix `461d2e77e229e940`, 4,692 lines |
| storage conformance | `packages/durable/src/testing/storage-conformance.ts`, sha256 prefix `a7925e4edbbb1eb7` |
| scenarios | `packages/durable/test/examples/00-*.ts` through `31-*.ts` (32 programs) |
| SQLite schema | `src/storage/sqlite/migrations.ts`, schema version 1 |

A pin bump is a contract change: re-capture the corpus (section 6) at the new version, diff the two captures, and treat every changed row shape as a work item. The unreleased upstream changelog reportedly adds `startedAt`/`endedAt` to task records and `durationMs` to messages (research notes 3, unverified here). That is exactly the case section 2.3 exists for.

## 2. What "same" means

### 2.1 Equivalence levels

| level | definition | required |
|---|---|---|
| `store` | After the scenario, every Pi table (`durable_schema`, `durable_metadata`, `record_ids`, `conversations`, `entries`, `tasks`, `submissions`, `documents`, `document_revisions`) holds the same rows, column by column, byte for byte. Tables named `pig_*`, Cloudflare's internal `_cf_*` tables (workerd creates `_cf_KV`, `_cf_METADATA` and `_cf_EXTERNALS`; the DO docs call the KV table `__cf_kv`) and SQLite's `sqlite_*` tables are excluded. | yes |
| `commit` | For every `next_seq` value `s`, the row changes made by commit `s` (inserts, updates with before/after images, deletes) are equal. It catches intermediate task states, superseded deltas, and commit grouping that `store` cannot see. | yes |
| `statement` | Same SQL statements in the same order. | no |
| `context` | Every model call receives the same pi-ai `Context` (system prompt, messages, tools) and the same stream options, in the same call order. | yes |
| `output` | The scenario's observable host output (Harness API results, events, example stdout) is equal. | yes for upstream examples |

`statement` is deliberately not required. The core may omit Pi's read statements (it holds `next_seq` in memory) and may skip `INSERT OR IGNORE INTO record_ids` for IDs it knows exist. It must not change a single row.

### 2.2 Determinism instead of normalisation

Capture runs remove nondeterminism at its source, so the default normaliser set is empty:

| source | evidence | control |
|---|---|---|
| Harness clock | `HarnessOptions.now` (`src/harness/types.ts:458`) | bound to the virtual clock |
| progress throttles | tool output `Progress` reads `Date.now()` directly, not the Harness clock (`src/harness/output.ts:310,323`); generation partials wait on `setTimeout(flush, partialIntervalMs)` (`src/harness/generation.ts:372-414`) | virtual `Date.now`/`performance.now`/`setTimeout` |
| provider session id | `ProviderDoc.initial()` calls pi-ai `uuidv7()` (`src/harness/provider.ts:18`), which reads `Date.now()`, draws 16 bytes from `crypto.getRandomValues` per call and keeps process-global `lastOrdinaryTimestamp` and `sequence` state (`packages/ai/src/utils/uuid.ts`) | virtual clock; every `uuidv7()` value is recorded on the effect tape (section 5.3) and returned by the candidate host's `pig.uuidv7` import (ABI section 4.2). A seeded `getRandomValues` alone does not align the values, because other code in each process draws from it too and the sequence state outlives a Harness reopen in the same process |
| faux provider | `randomId()` uses `Date.now()` and `Math.random()` (`packages/ai/src/providers/faux.ts:163`); `tokenSize` draws from `Math.random()` (`faux.ts:274`); message timestamps use `Date.now()` (`faux.ts:99,289,304,318,327`) | seeded `Math.random`, `tokenSize.min == max`, virtual clock |
| faux usage | the faux provider caches the previous prompt per `sessionId` in process memory and derives `cacheRead`/`cacheWrite` from it (`faux.ts:240-254`) | restart points are part of the scenario (section 2.4) |
| commit interleaving | parallel tools, sub-agents and background tasks settle in effect-completion order | effect completions are scripted (section 5.3) |

A value that cannot be controlled gets a named normaliser in the scenario manifest with a one-line reason. The spike's two normalisers (`"timestamp":\d+` and `faux:\d+:[a-z0-9]+`, `bench/durable/wasm-spike/bench/dump.ts`) become unnecessary under the virtual clock and are not allowed in acceptance runs.

### 2.3 Unknown fields and other versions

1. Reading never fails on an unknown field, an unknown entry kind, or a different key order. The byte scanner (ADR D4) is a structural JSON tokenizer, not a substring search.
2. Records the core does not rewrite (entries are immutable) are passed through as stored bytes.
3. Records the core rewrites (task replacements, submission updates, document bases and deltas, `durable_metadata`) follow what pinned pi-durable does with the same input: if 1.0.4 carries a field through a spread, the core carries it byte for byte in the same position; if 1.0.4 drops it, the core drops it. The corpus proves this with mutation scenarios that inject unknown fields into a store before the run (section 6, X1).
4. A store written by a newer schema version (`durable_schema.version > 1`) is refused exactly as `applySqliteMigrations` refuses it.

### 2.4 Restart points

Scenario state includes process lifetime. Usage values differ when a process restarts because the faux provider's prompt cache is in memory, and `uuidv7()`'s sequence state is per process; a crash loses the uncommitted throttle window. Every scenario therefore declares its restart points (close/reopen, or crash after commit `k`), and both implementations restart at exactly those points. A comparison across different restart cadences is invalid, not a failure.

## 3. Semantics the core reproduces

Section numbers refer to `docs/spec.md`. "Bench slice" marks what the bake-off lanes implement (BAKEOFF.md section 2); everything else is required of the production core.

### 3.1 Commit rule (bench slice)

- One Session commit is one SQL transaction (§1 invariant 1, §11.2). The commit reads `durable_metadata.next_seq` as its sequence `s` and writes `next_seq = s + 1`: exactly one per commit (`storage.ts` `commit`).
- `s` is the value stored in `entries.commit_seq`, `document_revisions.seq`, `documents.created_at` and `documents.retired_at` for that commit. Row identity therefore pins every commit boundary: merging or splitting commits is a contract violation, not an optimisation. The researcher measured that collapsing a turn's 77 commits into one saved only 1-2 ms.
- IDs come from one global counter for every record type (§2, `mintId`). `next_id` is stored as TEXT (`migrations.ts`: node:sqlite safe-integer reason) and becomes `max(stored next_id, in-memory minted counter, max written id + 1)` at each commit, so IDs minted by a rolled-back callback still advance it.
- Every created conversation, entry, task, submission and document ID is claimed in `record_ids` with its type; a conflicting claim rejects before any durable effect. Pi runs `INSERT OR IGNORE INTO record_ids` on every table write, updates included (`claimId`, `src/storage/sqlite/storage.ts:775-777`), which is why a turn has 104 claims: one per task (84), entry (18) and submission (2) write.
- Indexed text columns (`tasks.kind`, `submissions.request_id`, `documents.kind`, `documents.key_value`) hold `JSON.stringify(value)`, quotes included (`encodeIndexedString`).
- Record columns hold `JSON.stringify` output: insertion key order, ECMAScript number formatting (`-0` prints `0`, exponent form from `1e21` and below `1e-6`), only `"`, `\` and C0 controls escaped, lone surrogates as `\udXXX` escapes, U+2028/U+2029 and non-ASCII unescaped. Go's `encoding/json` violates several of these and is not used for records.
- Documents: one base or delta per changed incarnation per commit (§10); a latest-history document that writes a base first deletes its older revisions (`DELETE FROM document_revisions WHERE document_id = ?`, 35 per measured turn). Delta content is Chord `Op[]` JSON and must equal what `@earendil-works/chord` 1.0.4 emits, including its string trim/append detection and 64 KiB overlap search (§8.2).

### 3.2 Turn protocol (bench slice)

An eight-tool durable-bench turn is 77 commits and 372 write statements at every history size (captured from pi-durable 1.0.4 in Miniflare; confirmed independently by the durable-perf lane). Breakdown (research notes 3): 1 commit places the input (submission `placed`, `pi.user` entry, run in `pi.live`, generation task, per the §6 admission table); 9 per tool-calling generation (prepare, request, live delta, response, tool call, tool execute, result, generation resumes, next generation queued); 4 for the final answer. The exact contents of each commit come from the capture, not from this list. Statement shapes per turn: 104 `record_ids`, 84 `tasks`, 77 `durable_metadata`, 52 `document_revisions` inserts, 35 revision deletes, 18 `entries`, 2 `submissions` (`results-capture-3500.json`). Rules: §6 admission and boundaries, §8.2 live document, §8.3 generation phases `prepare/request/retry/poll/tools`, §8.4 tool phases `call/execute`, §8.5 rounds (sequential vs parallel, `pending`), §8.6 usage ledger (complete base on every change).

### 3.3 Context derivation (bench slice: all nine rules)

§2.1, in order: newest head marker `H` at or before the cutoff bounds the range, which starts at the entry `H.head` names, not at `H`; newest edit per target wins (`omit`, `replace`); the marker entry leads; positional system messages keep their section and tool deltas; tool results move directly behind their call in call order, ahead of intervening user or system messages; calls without a result get a synthesized error result, results without a call are dropped; model-less entries and assistant messages with `aborted`, `error` or `deferred` stop reasons are excluded. Fork traversal is child entries then parent entries through each `parent.at` cap. The offered tool set is replayed with pi-ai `getCurrentTools()` over the derived messages (§7.4 step 1; `src/harness/prompt.ts:85`, `src/harness/generation.ts:554`).

The whole derivation is one ~100-line function in Pi (`src/harness/context.ts:59-82` and `orderToolResults` at `:129-151`), so the bench slice implements every rule. Rule 6 applies to every Harness conversation, because the first preparation appends a positional `pi.system` baseline (§7.4); rule 8 is part of the same pass as rule 7 (`orderToolResults`, missing-result text at `:9`); rule 4 applies to edits carried by W4a's head markers.

### 3.4 Tasks

§5: states `pending/running/waiting/completing/terminal`; the six step rules applied before every phase; reservation, reconciliation on open (`running → pending`, abort marks kept), handover, migration at reservation, blocked tasks for missing or older definitions; memos (first writer wins, dropped at terminal); `waiting` with `failFast`/`allSettled` joins; `completing` while owned work is live (§5.5); scheduler-written faults and orphaning (§5.4); background tasks. Custom task phase handlers are host code (ADR D12); the scheduler and its rules are core code.

### 3.5 Documents

§3: definitions, scopes (`session`, `conversation`, `task`), history `latest`/`rewindable`, fork policy, bases and checkpoints, versions and migrations, copy on fork (`document.copy` at the source's stored version), retirement. Built-ins: `pi.live` (§8.2), `pi.usage` (§8.6), `pi.inbox` (§6), the agent document with `tools` (§8.5), the provider document with `sessionId` (1.0.2 changelog).

### 3.6 Hooks and extensions

§7.2 chains with their failure rules: `beforeRequest` (replacement chain), `afterResponse` (observers), `onYield` (first continuation wins), `beforeTool` (argument replacement, first block wins), `afterTool` (result replacement), `afterTools` (observers), `beforeCompact` (first decision wins). §7.1 registry snapshots per phase; §7.3 tools (validation through pi-ai `validateToolArguments()`, whose converted arguments are stored in the `execute` checkpoint (section 5.6), replay policy, output window, diagnostics, spill); §7.4 system prompt sections and dynamic tools as `pi.system` deltas with `content: ""`; §7.5 reload in place with handover at the next phase boundary.

### 3.7 Forks, compaction, inbox, sub-agents

- Forks: §3.7 and §2 `parent.at`; a fork starts with an empty inbox and a zero usage ledger.
- Compaction: §8.7 range selection, summary entry with `head` at the first kept entry, blocking threshold (`contextWindow - reserveTokens`), background threshold, overflow recovery (§8.3: one compaction-backed retry, not counted against the retry policy), stale summaries under §6's head-write rule.
- Inbox: §6 admission table, `requestId` deduplication, `whenBusy`, steering and follow-up modes, boundary selection order (writes, then user items, by ID), stale head writes, reset at `postTools` behaving as `final`.
- Sub-agents: owned conversations (§2 ownership), foreground and background patterns (examples 22, 23), child tasks (example 24), subtree abort and idle traversal.

### 3.8 Recovery (bench slice for generation and tool)

On open: reconciliation (§5.1). Generation `request` converts a committed partial into an aborted `pi.assistant` entry before re-requesting with the pinned model, thinking level and stream options and reruns `beforeRequest`. Tool `execute` re-executes only when stored and current replay policies are both `safe`; otherwise it commits an `interrupted` result from the slot's durable partial output and ends `failed`. Fault and orphan settlement convert partials like abort does (§8.3, §8.4). The wasm-spike lane reproduced these rules for the bench workload and passed crash handoff at all 77 commit boundaries at 50/250/1,000/3,500 turns (handoff `durable-wasm-spike.md`, update 3).

### 3.9 Streaming partials

Generation and tool output use two different throttles; both commit trailing writes with one commit in flight and coalesce changes made meanwhile.

- Generation partials (§8.3, `streamResponse`, `src/harness/generation.ts:360-414`): events of type `done` or `error` and partials with empty `content` are skipped; the first partial arms `setTimeout(flush, partialIntervalMs)` (no immediate commit); `flush` commits a `copyJson` of the newest pending partial into `pi.live.generation.message`; when that commit settles and a newer partial is pending, the next flush is armed `partialIntervalMs` after the settlement. There is no size term. The `finally` block stops the throttle, clears the timer and awaits the commit in flight before classification, so no stale partial lands after the outcome.
- Tool output (§7.3, §8.2, `Progress` in `src/harness/output.ts:268-340`, created at `src/harness/tool.ts:287`): the first change after an idle period commits at once; each commit then delays the next to `started + max(outputIntervalMs, bytes * 1000 / PROGRESS_BYTES_PER_SECOND)` with `PROGRESS_BYTES_PER_SECOND = 100 KiB` (`output.ts:261`), measured from the commit's start; `details()` resolves only when the commit that includes it settles (`markAndWait`, `tool.ts:211-216`); `stop()` awaits the commit in flight.

Defaults are 100 ms (`settings.progress`, `src/harness/agent.ts:34-35`, 1.0.3 changelog). durable-bench never streams; the contract corpus does (section 6, W3).

### 3.10 Storage read contract

The host serves Harness reads that do not need core state (old history pages, task and submission lookups) straight from Pi's tables. Those reads must satisfy §10 exactly. The core's write encoder, wrapped as a `Storage`, must pass the upstream storage conformance suite unchanged, which proves every `StorageWrite` type independently of the Harness.

## 4. What the core may differ in

Only non-observable behaviour: statements issued, reads, memory layout, time spent, the `pig_*` sidecar tables (ADR D6, recorded as an additive feature when they land), and log output. Anything a Pi Harness client, a pi-durable process opening the same store, or a provider can observe is in scope of sections 2-3. A difference that must stay gets a numbered entry in `docs/parity/DIVERGENCES.md` before it lands.

## 5. How it is checked

### 5.1 Capture

pi-durable runs every scenario over a capturing SQLite adapter (`capsql`, section 7.1). The adapter implements pi-durable's `SqliteDatabase` facade over `node:sqlite`, records every statement with its parameters and transaction boundaries, and records the row changes of each committed transaction. The capture output is a trace (section 7.6). The same scenario then runs against the candidate (Wasm core in a JS host, native Go core, TS control) with the same deterministic environment and the same effect script, producing a trace in the same format.

### 5.2 Comparison

`rowdiff` (section 7.4) compares two traces at the `store`, `commit`, `context` and `output` levels and reports the first divergent commit with a field-level JSON diff of each differing record column. A key-order-only difference and a number-format-only difference are reported as such, because both break `store` although the decoded values match.

### 5.3 Effect script

Model responses, tool results, hook decisions and their completion order are inputs. The capture records them as an effect tape: for each effect, its request (context fingerprint, tool call) and its completion (events, result, virtual time of completion). The candidate run replays the tape and fails on the first request that differs from the recorded one (a `context` failure). Without the tape, parallel rounds and sub-agents are not comparable at the `commit` level.

Pi itself must be deterministic under the tape: every scenario is captured five times and the five traces must be `commit`-equal. A scenario that is not is marked `pi-nondeterministic`, compared at the `store` level only, and reported upstream.

### 5.4 Context fingerprints

Two fingerprints per model call:

- `ctx`: sha256 of the canonical JSON of the pi-ai `Context` and stream options passed to `models.streamSimple` (Pi: wrap `models`; candidate: the `model_context` effect payload, ABI section 6). In 1.0.4 that `Context` is `{ messages }` only (`src/harness/generation.ts:402`); the system prompt and tools travel inside it as positional `pi.system` messages (§7.4). The options' `signal` is not hashed. This is the `context` check.
- `bench`: durable-bench's own `fingerprint()` (8 bytes of sha256 over `[role, text, calls]`, `src/plan.ts`). Known seed fingerprints: 50 `b017b487524e44a4`, 250 `dcea9f30b0917245`, 1,000 `ac520308146f2a8f`, 3,500 `0a8c8e4b0d9a0794` (bench-pig handoff). Required for both scripted-model variants.

For the wire plane (core-built provider bodies, ADR D11) a third fingerprint hashes the HTTP request body and must equal the body pi-ai 1.0.4 builds for the same context and model.

### 5.5 Handoff matrix

For each handoff scenario and each crash point `k` (after commit `k`, every commit of one measured turn; streaming scenarios include every partial commit):

| order | run 1 (to crash or restart) | run 2 (recover and finish) |
|---|---|---|
| control | pi-durable | pi-durable |
| P→X | pi-durable | candidate |
| X→P | candidate | pi-durable |
| X→X | candidate | candidate |

Each non-control order must end `store`-equal to the control, and the commits of run 2 must be `commit`-equal to the control's run 2. Crash means the process is abandoned after commit `k` without `close()`. A clean restart (close, reopen) is the `k = end` row. Sizes: 50 and 1,000 for every `k`; 3,500 for a sample of 10 `k` values including the first and last.

### 5.6 Byte-level differential tests

- Encoder: random JSON values (including lone surrogates, `-0`, large and tiny floats, U+2028, deep nesting, insertion orders) encoded by the core's encoder equal `JSON.stringify`.
- Scanner: random and adversarial records (escaped quotes, nested `role` keys inside tool arguments, images, unknown fields, any key order) yield the same index as `JSON.parse`-based extraction. The spike's `bench/verify-scan.ts` is the seed.
- Chord: random document edit sequences produce the same `Op[]` as `@earendil-works/chord` 1.0.4.
- Tool arguments: the arguments stored in a tool task's `execute` checkpoint are the output of pi-ai `validateToolArguments()` (`packages/ai/src/utils/validation.ts:317-350`: `structuredClone`, `normalizeOptionalNulls`, TypeBox `Value.Convert`, and for plain JSON schemas `coerceWithJsonSchema` with key reassignment, which can reorder keys), and its error text becomes a stored result. Random arguments against the corpus's tool schemas must produce equal bytes and equal error text.
- `uuidv7()`: the native host's Go port equals pi-ai 1.0.4 for the same clock and random bytes, including the sequence carry across calls in one process.

These run as Go fuzz tests with a Node oracle corpus checked into testdata.

## 6. Corpus

| id | source | adaptation | checks |
|---|---|---|---|
| E00-E31 | the 32 upstream examples | storage swapped to `capsql` by an ESM resolve hook (the `MemoryStorage` constructor and `openNodeJsonlStorage` return a SqliteStorage over `capsql`; `openNodeSqliteStorage` gets `capsql` directly); `OPENAI_API_KEY` unset so 18, 19, 22 and 23 take their faux path; 16 (real model) replays a recorded cassette and is skipped until the owner records one; examples using `NodeExecutionEnv` record tool results on the tape; candidate runs alias `../../src/index.ts` to the candidate's JS API (ADR D12) | store, commit, context, output |
| U | upstream `test/*.test.ts` harness tests (47 files) | the Go ports under `durable/...` (for example `spec_usage_upstream_test.go` and `durable/examples`, as tracked in the test mapping) run against the core-backed Harness once it exists | their own assertions |
| SC | `src/testing/storage-conformance.ts` | the core's write encoder wrapped as `Storage` | suite |
| I | `durable/interop` scripted session (documents, task-starting tool, fork with init, compaction, reset) | unchanged; extended with a candidate column | store, commit, cross-open |
| W1 | durable-bench, published scripted model (full transcript walk per call) | fixtures 50/250/1,000/3,500 (`fx/pi-node-*.sqlite`, Pi-written), ten 8-tool measured turns, open/close per sample | store, commit, context, bench fingerprint |
| W2 | durable-bench, incremental scripted model | same fixtures; per-call work O(new messages); must produce W1's fingerprints | as W1 |
| W3 | streaming | faux provider streaming each response in 40-60 chunks over 1-2 s virtual time; `partialIntervalMs` 100 and 250; one run with tool output progress | store, commit, context, crash at every partial commit |
| W4 | real-turn mixes | W4a compaction-bounded history (fixtures written by pi-durable with compaction on and a 200k-token window, so head markers bound the range; the measured turns run with the compaction policy disabled for every implementation, so `prepare` checks no threshold (§8.3) and the bench slice never starts a compaction) at 1,000 and 3,500 turns; W4b every turn cold (runtime restarted between turns); W4c 20-50 KB bounded tool outputs; W4d a parallel round of 3 tools; W4e a foreground sub-agent; W4f recorded OpenAI Responses and Anthropic Messages streams (wire plane); W4g a retryable provider error then success | store, commit, context |
| X1 | mutation | W1 at 50 with unknown fields injected into one entry, one live task and one document base before the measured turns | store, commit |
| H | handoff | W1 at 50/1,000/3,500, W3, W4 parallel round | section 5.5 |

The bench slice must pass W1, W2, W3, W4a-c, X1, and H on W1 and W3. The production core must pass the whole table.

## 7. Tooling to build

All Node tooling lives in `durable/contract/` with `package.json` pinned like `durable/interop` (pi-durable, pi-ai and chord 1.0.4) and runs on the Node version that durable-bench uses. Go tooling lives next to the core (`durable/core/contracttest`).

### 7.1 `capsql.mjs`

`SqliteDatabase` and `SqliteExecutor` implementation over `node:sqlite` `DatabaseSync`. Records `{tx: begin|end|rollback}`, `{q, p}` for every statement, and for each committed transaction the row changes. Row changes come from a SQLite session (`DatabaseSync.createSession()`, changeset decoded per table) when the Node build supports it, else from before/after snapshots of the touched rows by primary key. `createSession()` and `changeset()` work on Node 26.7.0, the durable-bench host's Node (probed during review). Open one session per transaction and take its changeset at commit: a session merges every change to a row into one change, and an UPDATE change carries old values only for the changed columns and the primary key, so full `before` images need the snapshot path. Every Pi table has a primary key, which the session extension requires. The same adapter serves Miniflare captures by replaying the recorded statements into `node:sqlite` (the DO SQL API has no session extension).

### 7.2 `detenv.mjs`

Installed with `--import` before any scenario module. Virtual clock starting at a fixed epoch: `Date.now`, `performance.now`, `setTimeout`/`setInterval`/`clearTimeout` on a virtual timer queue that advances to the next due timer only when the microtask queue is empty and no real I/O is pending (`process.getActiveResourcesInfo()` minus the virtual timers). Seeded `Math.random`, `crypto.getRandomValues`, `crypto.randomUUID`. Exposes the clock to `HarnessOptions.now`.

### 7.3 `tape.mjs`

Wraps `Models` (`streamSimple`, `fetchDeferred`, `cancelDeferred`) and tool `execute` calls. Record mode stores requests, fingerprints, streamed events with virtual timestamps, results and completion order. Replay mode serves them and fails on a request mismatch. A ready-made faux response factory provides the durable-bench script for W1/W2.

### 7.4 `rowdiff.mjs`

`rowdiff a.trace b.trace [--level store,commit,context,output]`. Exit status non-zero on any difference. Report: first divergent commit seq; per table, rows only in A, only in B, and changed rows with a JSON-aware diff of TEXT columns that classifies value, key-order, number-format and escape differences. `--json` emits a machine-readable report for CI.

### 7.5 `matrix.mjs`

Drives section 5.5: for a scenario and implementation pair, enumerates crash points from the control trace, runs each order in fresh temp directories, and summarises pass/fail per `k`. Supports `--impl pi|wasm|native|ts` and `--host node|miniflare`.

### 7.6 Trace format

JSON Lines, one object per event, in order:

```text
{"t":"meta","scenario":"W1-50","impl":"pi-durable@1.0.4","pin":"7c10bd43","clock":0,"seed":1}
{"t":"commit","seq":4127,"stmts":[{"q":3,"p":[...]}],"changes":[{"table":"tasks","op":"update","pk":[812],"before":[...],"after":[...]}]}
{"t":"model","call":17,"ctx":"<sha256>","bench":"<8 bytes hex>"}
{"t":"restart","kind":"crash","after":4127}
{"t":"out","api":"submission.wait","value":{...}}
{"t":"final","tables":{"entries":"<sha256 of ordered rows>",...}}
```

`q` indexes a statement-text table in the meta line. `final` carries per-table digests so the `store` level can be checked without the full store; the store file is kept next to the trace for diffing.

### 7.7 Scenario manifest

`durable/contract/corpus.toml` lists each scenario: id, source path and sha256 at the pin, adaptation hooks, restart and crash points, allowed normalisers with reasons (empty by default), required levels, and the `pi-nondeterministic` flag. `make durable-contract` runs the manifest against a named implementation; CI runs the bench-slice subset.

### 7.8 Existing seeds

Reuse rather than rewrite: the spike's `pix.ts` counters and `capture.ts` (bundle `pig-do-spike.tar.gz`), the wasm-spike lane's `bench/{verify,handoff,dump,verify-scan,recovery-trace}.ts` and `src/engine.ts`, the bench-pig lane's fixtures and fingerprints, and `durable/interop/{scenario,resume,rawdump}.mjs`.
