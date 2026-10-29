#!/usr/bin/env node
// Generate corpus.toml from the reference checkout (CONTRACT section 7.7). The hand-written parts (owners, families, the
// bench rows) live in this file; the sha256 of every source file at the pin is read from the checkout, so a pin bump
// regenerates the manifest and `contract corpus --check` shows what changed.
import { createHash } from "node:crypto"
import { readdirSync, readFileSync, writeFileSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { piSource } from "../lib/run.mjs"

const pi = piSource()
const sha = rel => createHash("sha256").update(readFileSync(join(pi, rel))).digest("hex")
const q = s => JSON.stringify(s)

// Owner lanes (the production-core lanes of docs/plan/durable-core). session: Session kernel, record encoding, documents,
// tasks, scheduler, inbox, sub-agents. loop: generation, tools, prompt and context derivation, output, usage. store: records,
// the store, compaction, edits, forks, reset. host: hosts, the JS facade, bundles.
const E = {
	"00-conversation": ["session", "a Session stores conversations, entries, tasks, documents"],
	"01-documents": ["session", "documents next to transcript entries"],
	"02-forks": ["store", "fork a conversation"],
	"03-owned-conversations": ["session", "a background task that owns a child conversation"],
	"04-watches": ["session", "serialize asynchronous document work with a watch"],
	"05-harness": ["loop", "open a Harness with a registry of extensions"],
	"06-configuration": ["loop", "per-conversation agent choices and Harness settings"],
	"07-harness-conversations": ["session", "conversation handles, typed entries, created conversations, forks"],
	"08-context": ["store", "transcript history and model context"],
	"09-registry-reload": ["loop", "reload extension code through the registry"],
	"10-extension-state": ["loop", "an extension's per-conversation document"],
	"11-tasks": ["session", "run a durable task"],
	"12-recovery": ["session", "close, reopen, continue where a task stopped"],
	"13-chat": ["loop", "a chat turn"],
	"14-system-prompt": ["loop", "system prompt sections and instructions"],
	"15-real-model": ["loop", "a real model (OpenAI); no key, no cassette: skipped"],
	"16-coding-tools": ["loop", "a coding-agent tool turn"],
	"17-print": ["loop", "print mode, faux path"],
	"18-json": ["loop", "JSON mode, faux path"],
	"19-inbox": ["session", "the inbox while a conversation is busy"],
	"20-late-join": ["session", "a client attaches mid-run"],
	"21-subagent-foreground": ["session", "a foreground subagent tool"],
	"22-subagent-background": ["session", "persistent background subagents"],
	"23-child-tasks": ["session", "a task that owns child tasks"],
	"24-compaction": ["store", "compaction of a long chat"],
	"25-coding-agent": ["loop", "a small coding agent"],
	"26-plan-mode": ["loop", "plan mode"],
	"27-reviewer": ["loop", "a reviewer agent"],
	"28-sandbox-per-conversation": ["loop", "one sandbox per conversation"],
	"29-tool-override": ["loop", "replace and decorate a tool"],
	"30-reload-and-restart": ["session", "reload an extension while a call runs, then restart"],
	"31-transcript": ["session", "a client keeps a conversation's transcript over the windowed view"],
}
const U = {
	"codemode-checkpoints": "loop", "harness-blocked": "session", "harness-deferred-progress": "session", "harness-failed-run-inbox": "session", "harness-host-inputs": "session",
	"harness-json": "loop", "harness-observers": "session", "harness-partial-changes": "session", "harness-read-only": "session", "harness-reports": "session", "harness-sleep": "session",
	"harness-telemetry": "session", "harness-telemetry-options": "host", "indexeddb-storage": "host", "session-adoption": "session", "session-deferred-commits": "session", "session-observers": "session",
	"codemode-describe": "loop", "codemode-models": "loop", "codemode": "loop", "tool-search": "loop", "env-line-scan": "host", "env-node-conformance": "host", "env-node-spill": "host", "env-node": "host", "env-truncate": "host",
	"images-wasm-module": "host", "tools-images": "loop", "harness-callers": "loop", "harness-cancellation-barrier": "session", "harness-scheduler-indexes": "session", "harness-scheduler-retention": "session", "harness-nested-tools-restart": "loop", "harness-nested-tools": "loop",
	"harness-storage-failure": "session", "harness-structured-output": "loop",
	"harness-compaction": "store", "harness-context": "store", "harness-conversations": "session", "harness-generation-recovery": "loop", "conversation-transcript": "session", "harness-restart-failure": "session", "harness-run-failure": "session", "harness-view-run": "session",
	"harness-generation": "loop", "harness-inbox": "session", "harness-input-entries": "session", "session-document-retention": "session", "harness-inspect": "session", "harness-lifecycle": "session", "harness-live-deltas": "loop",
	"harness-ownership": "session", "harness-prompt": "loop", "harness-registry": "loop",
	"harness-structured": "session", "harness-submissions": "session", "harness-task-graph": "session", "harness-tasks-recovery": "session", "harness-tasks": "session",
	"harness-tools-recovery": "loop", "harness-tools": "loop", "harness-view": "session", "jsonl-storage": "host", "memory-storage": "host",
	"provider-session-cache-e2e": "loop", "session-checkpoints-migrations": "session", "session-definitions": "session", "session-documents": "session",
	"session-forks": "store", "session-tables": "store", "session-watches": "session", "spec-usage": "store",
	"sqlite-cloudflare": "host", "sqlite-facade": "host", "sqlite-migrations": "store", "sqlite-storage": "store", "storage-runtime-boundary": "store",
	"system-order-cache-e2e": "loop", "tools-read-differential": "loop", "tools": "loop", "types": "store",
}
const out = []
const emit = (...lines) => out.push(...lines, "")
emit(
	"# The scenario manifest (CONTRACT section 7.7). Generated by tools/gen-corpus.mjs from the reference checkout; the hashes pin each",
	"# source at the reference commit. `contract corpus --check` fails when a source drifted. Owners are the production-core lanes",
	"# (store, session, loop, host); a failing row is reported against its owner.",
	"#",
	"# levels: store (every Pi table byte for byte), commit (each commit's row changes), context (the pi-ai Context of every model",
	"# call), output (what the scenario prints), order (the scenario's API calls and the commits in one sequence, as Pi orders them by",
	"# microtask scheduling; CONTRACT 5.8). normalisers are empty: determinism comes from detenv, not from rewriting values.",
	"# status: ready (a runner exists and the reference capture is deterministic), pending (no candidate can run it yet, with the",
	"# reason and the owner; `--strict` fails on it), skipped (nobody can run it here, with the reason).",
	"",
	"[pin]", `reference = ${q("earendil-works/pi")}`, `commit = ${q("b3a29f1111114fac696e2cfcd65acdb382a6ed5e")}`, `release = ${q("1.0.4")}`,
)
for (const f of readdirSync(join(pi, "packages/durable/test/examples")).filter(f => /^\d\d-.*\.ts$/.test(f)).sort()) {
	const name = f.replace(/\.ts$/, "")
	const [owner, about] = E[name]
	const skip = name === "15-real-model"
	emit("[[scenario]]", `id = ${q("E" + name.slice(0, 2))}`, `family = "example"`, `runner = "example"`, `source = ${q("packages/durable/test/examples/" + f)}`,
		`sha256 = ${q(sha("packages/durable/test/examples/" + f))}`, `owner = ${q("dcore-" + owner)}`, `about = ${q(about)}`,
		`levels = ["store", "commit", "context", "output", "order"]`, `normalisers = []`, ...(skip ? [`status = "pending"`, `reason = "pending-owner: needs the owner's OPENAI_API_KEY or a cassette recorded with it (CONTRACT section 6)"`] : [`status = "ready"`]))
}
// U files that cannot run against the core. Each carries its reason (lib/corpus.mjs requires one); a pass that never reaches the
// core must not count (dcore-int ruling, 2026-10-07). The Session files import ../src/session/session.ts (SessionImpl) and drive
// it over ControlledStorage: they stay pending until a candidate supplies session/session.ts over the core's Session mode, or
// the owner ports their cases to Go one for one; no other row exercises those cases.
// The conformance suite each of these files registers (registerStorageConformance, the same suite for every Storage class) runs against
// the core as row SC: the core's write encoder wrapped as a Storage (CONTRACT 3.10). What is left in the file tests that one class's
// own internals (its file format, its schema upgrade, its prepared-commit handle), which the core replaces with its encoder and the
// host's SQL, so no candidate can run it without handing the reference's class back.
const REFERENCE_STORAGE = "the upstream storage conformance suite this file registers runs against the core as row SC (CONTRACT 3.10); the rest tests this Storage class's own internals, which no candidate can run without handing the reference's own class back"
const NOT_THE_CORE = {
	"memory-storage": REFERENCE_STORAGE, "jsonl-storage": REFERENCE_STORAGE, "sqlite-facade": REFERENCE_STORAGE, "sqlite-storage": REFERENCE_STORAGE, "sqlite-migrations": REFERENCE_STORAGE,
	"indexeddb-storage": REFERENCE_STORAGE,
}
// A row's real-time tests, with the measurement that shows the window binds the reference as well (gate.mjs reads real_time_tests).
// `exclusive` (gate.mjs runRows, runVitestRow): the gate runs the row alone, its reference run and its candidate run one after the other,
// because the window is filled by the run's own work and any other row's processes on the host stretch that work past it.
// Read-only: assigned once at initialization.
const REAL_TIME = {
	"harness-tools": [
		"# The test sleeps 20 ms (real setTimeout) inside the 100 ms output throttle window; a process stall longer than ~80 ms flushes the",
		"# buffered output in the reference as well as in the candidate (checked with an 85 ms stall: both fail; 70 ms: both pass).",
		`real_time_tests = ["tool progress and lifetime answers an aborted tool with only its durable output"]`,
		`exclusive = true`,
	],
	"codemode": [
		"# `times a script out across a restart, without admitting new calls` gives the script 300 ms (`@options timeout_ms`) from the start of its",
		"# first run to its first tool call: the sandbox starts and `hold` is reached, 148-207 ms of CPU on both sides (probed inside the gate). A",
		"# host that gives the process under ~65% of a CPU in that stretch ends the script live (\"Script timed out\") before `hold` runs,",
		"# `holds.reached` never resolves and the test fails at vitest's 5 s: in the reference as well as in the candidate (of six full runs at",
		"# -j 8 the candidate missed the window in four and the reference in one; alone the reference took 166-227 ms of wall time, the candidate 195-245).",
		`exclusive = true`,
	],
}
// A row's measured value of an approved divergence (gate.mjs reads measured_*): the candidate fails the test with this failure, and only with it.
const MEASURED = {
	"harness-restart-failure": [
		"# D206 (owner approved 2026-10-28): the core resolves HarnessOptions.settings from the snapshot the host sends before each event, so the",
		"# test's count of the reads of `extensions` (Pi: 49 a round, one per resolution at each use) is the 6 agent resolutions of the host in a round.",
		"# The candidate must fail this test with this failure: a pass means D206 is over (gate.mjs), another failure fails the row.",
		`measured_test = ${q("the restart limit on the normal path (#11139) resolves the settings no more often than before: only a task that was cut off reads the limit")}`,
		`measured_failure = "expected 6 to be 49"`,
		`measured_divergence = "D206"`,
	],
}
for (const f of readdirSync(join(pi, "packages/durable/test")).filter(f => f.endsWith(".test.ts")).sort()) {
	const name = f.replace(/\.test\.ts$/, "")
	emit("[[scenario]]", `id = ${q("U-" + name)}`, `family = "upstream-test"`, `runner = "vitest"`, `source = ${q("packages/durable/test/" + f)}`, `sha256 = ${q(sha("packages/durable/test/" + f))}`,
		`owner = ${q("dcore-" + U[name])}`, `levels = ["tests"]`, ...(NOT_THE_CORE[name] ? [`status = "skipped"`, `reason = ${q(NOT_THE_CORE[name])}`] : [`status = "ready"`]), ...(REAL_TIME[name] ?? []), ...(MEASURED[name] ?? []))
}
emit("[[scenario]]", `id = "SC"`, `family = "storage-conformance"`, `runner = "storage-conformance"`, `source = "packages/durable/src/testing/storage-conformance.ts"`, `sha256 = ${q(sha("packages/durable/src/testing/storage-conformance.ts"))}`,
	`owner = "dcore-store"`, `about = "the core's write encoder wrapped as a Storage passes the upstream storage conformance suite (CONTRACT 3.10): the suite registered by memory-storage, jsonl-storage, sqlite-storage, sqlite-cloudflare, sqlite-facade and indexeddb-storage"`, `levels = ["tests"]`, `status = "ready"`)
emit("[[scenario]]", `id = "I"`, `family = "interop"`, `runner = "interop"`, `source = "durable/interop/scenario.mjs"`, `owner = "dcore-host"`,
	`about = "documents, a task-starting tool, a fork with init, compaction and a reset, then the Go port reads the store"`, `levels = ["store", "commit", "output", "order"]`, `status = "ready"`)
// F: differential ordering. Each seed draws a random schedule of parallel tools, hooks, observers and inbox arrivals
// (scenarios/diff-ordering.mjs); Pi and the candidate play it and every row, commit, model context, printed line and the order of
// API calls and commits must be equal. One row per seed and storage-backend hop profile: `gate` is the reference's SqliteStorage
// over the capture (the profile the other corpus rows use), `node` the reference's own Node SQLite adapter (CONTRACT_STORAGE_PROFILE=node).
// `contract diff --seeds A-B` runs further seeds without rows.
export const DIFF_SEEDS = 16
// Seeds beyond 16 that each found a bug the first 16 left open: a built-in tool task's readCall (`runtime.entry`) is a line job of
// storage.entry(conversationId, id) (scheduler.ts :1216-1228), so the parallel tools' hooks start one read job apart; the core charged
// it nothing, and a blocked call's terminal commit then ran after another call's first revision (gate seeds 18, 26, 61, 67, 77).
export const DIFF_EXTRA_SEEDS = [18, 26, 61, 67, 77, 175]
for (const profile of ["gate", "node"]) for (const seed of [...Array.from({ length: DIFF_SEEDS }, (_, i) => i + 1), ...(profile === "gate" ? DIFF_EXTRA_SEEDS : [])]) {
	emit("[[scenario]]", `id = ${q(`F-${profile}-${seed}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/diff-ordering.mjs"`, `seed = ${seed}`, `profile = ${q(profile)}`, `owner = "dcore-loop"`,
		`about = ${q(`seed ${seed}: random parallel tools, hooks, observers and inbox arrivals on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-ticks: the tick cost of each public client call (scenarios/client-ticks.mjs), compared as printed counts (output level).
for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-ticks-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/client-ticks.mjs"`, `seed = 0`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes each client call spends (submit, wait, entries, inspect, abort) on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-ticks-tools: the pass at which each of three parallel tool calls runs its beforeTool hook, `execute`, return and afterTool hook
// (scenarios/tool-start-ticks.mjs): the gap between two calls' hooks decides whose hook wait starts first (F-gate seeds 26, 61, 67, 77).
for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-ticks-tools-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/tool-start-ticks.mjs"`, `seed = 0`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes between the beforeTool hook, execute, return and afterTool hook of three parallel tool calls on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-split: the passes between observable points inside one call (scenarios/split-ticks.mjs; the seed picks the part), so two tick
// errors that cancel in an F-ticks total fail apart: generation (the turn's model call and last events after its publication),
// client (the publication before `submit` resolves, `wait` after the turn's last event) and context (a custom task's runtime.context
// on a scanned, kept, cut and extended range).
for (const [seed, part, about] of [[1, "generation", "a turn's model call and last events, counted from the submission's publication"], [2, "client", "the submission's publication before submit resolves and wait after the turn's last event"], [3, "context", "a custom task's runtime.context on a scanned, kept, cut and extended range"]]) for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-split-${part}-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/split-ticks.mjs"`, `seed = ${seed}`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes between points of one call: ${about}, on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-ticks-runtime: the tick cost of the calls F-ticks leaves out (scenarios/runtime-ticks.mjs; the seed picks the part): runtime.context
// in a custom task, client reads behind a running turn and a wait under an abortable context (seed 0), and the calls that differ from
// Pi today (seed 1, F-ticks-runtime-drift): waitForTask on a settled and a live custom task, runtime.getTask,
// and status behind a running turn.
for (const [seed, suffix, about] of [[0, "", "runtime.context in a custom task (scan, kept, extend, cut), client reads behind a running turn and a wait under an abortable context"], [1, "-drift", "waitForTask on a settled and on a live custom task, runtime.getTask in a custom task and a single-row client read behind a running turn"]]) for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-ticks-runtime${suffix}-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/runtime-ticks.mjs"`, `seed = ${seed}`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes of ${about} on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-ticks-watch: the microtask passes `watchEvents` and `Conversation.watch` take to settle on an idle conversation (scenarios/watch-ticks.mjs):
// an observer that attaches in fewer passes than Pi lands before a submit's commit Pi orders first (F-gate seed 175).
for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-ticks-watch-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/watch-ticks.mjs"`, `seed = 0`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes of watchEvents and viewState on an idle conversation on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-ticks-output: a tool's streamed chunk and its return (scenarios/output-ticks.mjs): passes from api.output() to the tool_execution_update event, and from the
// tool's return to the tool_execution_end event on an attached event stream. F-gate-11 (commits 87/88) orders a sibling's completion ahead of a chunk when the return costs fewer passes than Pi's.
for (const profile of ["gate", "node"]) {
	emit("[[scenario]]", `id = ${q(`F-ticks-output-${profile}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/output-ticks.mjs"`, `seed = 0`, `profile = ${q(profile)}`, `owner = "dcore-host"`,
		`about = ${q(`microtask passes from a tool's api.output() to its update event and from its return to its end event, on the ${profile} storage profile`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// F-fail: a Storage `commit` that throws fails the Session (Pi 42a SessionEnd; scenarios/storage-failure.mjs): the commit number k throws once,
// and the call that hit it, every later call and wait, the event stream and the Harness end exactly as Pi's do, with the same store behind.
for (const [profile, ks] of [["gate", Array.from({ length: 19 }, (_, i) => i + 2)], ["node", [3, 9, 15]]]) for (const k of ks) {
	emit("[[scenario]]", `id = ${q(`F-fail-${profile}-${k}`)}`, `family = "differential"`, `runner = "diff"`, `source = "durable/contract/scenarios/storage-failure.mjs"`, `seed = 0`, `profile = ${q(profile)}`, `args = ["--fail", "${k}"]`, `owner = "dcore-session"`,
		`about = ${q(`Storage commit ${k} throws: the Session fails, the first error wins, later calls and waits reject SessionFailed, the event stream and the Harness end failed (${profile} storage profile)`)}`, `levels = ["store", "commit", "context", "output", "order"]`, `status = "ready"`)
}
// T1: time travel on a long W1 history (scenarios/time-travel.mjs, robust-durable 602aea434c): locate turn h100, the context and the agent
// document as of it, eight scrubbed as-of contexts, and a fork there with one turn. Pi runs it first; each build equals it at every level.
for (const n of [250, 3500]) {
	emit("[[scenario]]", `id = ${q(`T1-${n}`)}`, `family = "timetravel"`, `runner = "diff"`, `source = "durable/contract/scenarios/time-travel.mjs"`, `seed = 0`, `profile = "gate"`, `fixture = ${q(`pi-main-${n}`)}`, `args = ["--at-turn", "100", "--scrub", "8"]`, `owner = "dcore-history"`,
		`about = ${q(`time travel on a ${n}-turn history: as-of context and agent document at turn h100, eight scrubbed as-of contexts, a fork there with one turn`)}`, `levels = ["store", "commit", "context", "output"]`, `status = "ready"`)
}
// The bench rows: params go to scenarios/bench.mjs. sizes are turns of history in the fixture.
const bench = (id, family, owner, about, params, extra = [], status = "ready") => emit("[[scenario]]", `id = ${q(id)}`, `family = ${q(family)}`, `runner = "bench"`, `owner = ${q("dcore-" + owner)}`, `about = ${q(about)}`, `levels = ["store", "commit", "context"]`, `params = ${params}`, ...extra, `status = ${q(status)}`)
for (const n of [50, 250, 1000, 3500]) bench(`W1-${n}`, "bench", "loop", `durable-bench, full-walk scripted model, ten 8-tool measured turns after ${n} turns of history, open/close per sample`, `{ fixture = "pi-main-${n}", turns = 10, tools = 8, variant = "full" }`)
for (const n of [50, 250, 1000, 3500]) bench(`W2-${n}`, "bench", "loop", `as W1 with the incremental scripted model; must produce W1's fingerprints`, `{ fixture = "pi-main-${n}", turns = 10, tools = 8, variant = "incr" }`)
for (const [n, ms] of [[50, 100], [50, 250], [1000, 100]]) bench(`W3-${n}-${ms}`, "streaming", "loop", `streaming: 40-60 chunk responses over 1-2 s virtual time, partialIntervalMs ${ms}, crash at every partial commit`, `{ fixture = "pi-main-${n}", turns = 2, tools = 2, variant = "full", stream = 2000, partialMs = ${ms} }`)
for (const n of [1000, 3500]) bench(`W4a-${n}`, "bench", "store", `compaction-bounded history of ${n} turns (fixture written with compaction on and a 200k-token window; measured turns run with compaction disabled)`, `{ fixture = "compact-${n}", turns = 10, tools = 8, variant = "full" }`, [`status_note = "fixture: cli/fixtures.mjs --compact (compaction on, 200k window, 8 KiB tool results)"`])
for (const n of [50, 1000, 3500]) bench(`W4b-${n}`, "bench", "store", `every turn cold: the runtime restarts between measured turns`, `{ fixture = "pi-main-${n}", turns = ${n === 3500 ? 3 : 10}, tools = 8, variant = "full", restartEachTurn = true }`)
for (const n of [50, 1000]) bench(`W4c-${n}`, "bench", "loop", `20-50 KB bounded tool outputs`, `{ fixture = "pi-main-${n}", turns = 4, tools = 4, variant = "full", payloadKb = 32 }`)
bench("W4d", "bench", "loop", "a parallel round of 3 tools", `{ fixture = "pi-main-50", turns = 4, tools = 3, variant = "full", parallel = true }`)
bench("W4e", "bench", "session", "a foreground sub-agent (the tool of example 22): a child conversation the tool call owns, on a history of 50 turns", `{ fixture = "pi-main-50", turns = 2, tools = 1, variant = "full", subagent = true }`)
emit("[[scenario]]", `id = "W4f"`, `family = "wire"`, `runner = "cassette"`, `owner = "dcore-loop"`, `about = "recorded OpenAI Responses and Anthropic Messages streams (wire plane, ADR D11)"`, `levels = ["store", "commit", "context"]`, `status = "pending"`, `reason = "pending-owner: needs provider cassettes recorded with the owner's API keys (RISKS-AND-QUESTIONS Q6)"`)
bench("W4g", "bench", "loop", "a retryable provider error, then success", `{ fixture = "pi-main-50", turns = 2, tools = 1, variant = "full", fault = "retryable-error" }`)
bench("X1", "mutation", "store", "W1 at 50 with unknown fields injected into one entry and the pi.usage and pi.live document bases before the measured turns (CONTRACT 2.3)", `{ fixture = "pi-main-50", turns = 10, tools = 8, variant = "full", inject = ["entry", "usage", "live"] }`)
emit("[[scenario]]", `id = "X1-live"`, `family = "mutation"`, `runner = "matrix"`, `owner = "dcore-session"`, `about = "W1 at 50 crashed at 12 points; unknown fields injected into the live tasks (record, state, checkpoint, input) and submission before recovery"`,
	`scenario = "W1-50"`, `crash_points = "12"`, `orders = ["P>X", "X>P", "X>X"]`, `inject_at_crash = ["task", "state", "checkpoint", "input", "submission"]`, `levels = ["store", "commit"]`, `status = "ready"`)
for (const [id, n, ks] of [["H-50", 50, "all"], ["H-1000", 1000, "all"], ["H-3500", 3500, "10"], ["H-W3", 50, "all"], ["H-W4d", 50, "all"]]) {
	emit("[[scenario]]", `id = ${q(id)}`, `family = "handoff"`, `runner = "matrix"`, `owner = "dcore-loop"`, `about = ${q(id === "H-W3" ? "crash at every partial commit of W3" : id === "H-W4d" ? "crash at every commit of the parallel round" : `crash after every commit of one measured turn at ${n} turns (${ks} crash points), P>X, X>P, X>X`)}`,
		`scenario = ${q(id === "H-W3" ? "W3-50-100" : id === "H-W4d" ? "W4d" : "W1-" + n)}`, `crash_points = ${q(ks)}`, `orders = ["P>X", "X>P", "X>X"]`, `levels = ["store", "commit", "context"]`, `status = "ready"`)
}
// R: a store written by Pi 1.0.4 (PIN `release`) stays readable. Main and the candidate each extend it with measured turns and
// recover it from crashes; the 1.0.4 record shapes lack main's task startedAt/endedAt and message durationMs, which a reader
// must treat as absent and a writer must not add to rows it does not rewrite.
bench("R-104-50", "legacy", "session", "W1 at 50 turns on a store written by Pi 1.0.4: the reference main and the candidate read it and extend it identically", `{ fixture = "v1.0.4-50", turns = 3, tools = 8, variant = "full" }`)
emit("[[scenario]]", `id = "H-R-104"`, `family = "legacy"`, `runner = "matrix"`, `owner = "dcore-session"`, `about = "crash after every commit of one measured turn on a 1.0.4 store (all crash points), P>X, X>P, X>X"`,
	`scenario = "W1-50"`, `fixture = "v1.0.4-50"`, `crash_points = "all"`, `orders = ["P>X", "X>P", "X>X"]`, `levels = ["store", "commit", "context"]`, `status = "ready"`)
writeFileSync(fileURLToPath(new URL("../corpus.toml", import.meta.url)), out.join("\n"))
console.log(`corpus.toml: ${out.filter(l => l === "[[scenario]]").length} scenarios`)
