# Durable core bake-off

Status: ready for lane dispatch, 2026-10-06. Design under test: [ADR-0001](ADR-0001-core-architecture.md). Shared host interface: [ABI.md](ABI.md). Acceptance: [CONTRACT.md](CONTRACT.md).

The bake-off picks the language and memory layout for one fixed design. It does not reopen ADR decisions D1-D9. Every lane implements the same slice behind the same ABI, runs the same workloads with the same runner on the same machine, and passes the same acceptance gates before its numbers count.

## 1. Lanes

| lane | what it is | hypothesis | main risk | stop early when |
|---|---|---|---|---|
| S0 | shared infrastructure: ABI 1 JS shim, durable-bench runner targets, contract tools for the slice (CONTRACT section 7), L0 empty target, B2 patch | none; every other lane depends on it | late delivery blocks all lanes | never; it ships first |
| L1 | standard Go 1.27.1 wasip1 reactor; the core holds bytes and index | the design alone wins cold and warm; 1.2-1.3 MB gzip and a 3.5 MB per-instance floor are acceptable | size, memory per object, Go byte-scan speed | it fails acceptance after the wasm-spike core is ported to ABI 1 (that would be a design flaw: escalate) |
| L2 | the L1 source compiled by TinyGo (`-scheduler=none`, default GC) | several times smaller (spike: 0.37 MB gzip) and near-zero runtime init with equal semantics | compiler gaps on PiG code, GC behaviour on large arenas | L1's source needs more than build tags and small shims to compile, or a contract check fails only under TinyGo |
| L3 | Go core with history bytes held by the JS host (`hist.*`, body recipes) | Go memory stays flat with history length; JS `ArrayBuffer`s are reclaimed on eviction while Wasm memory never shrinks | more logic and copies in the shim; cold scan through a scratch buffer | memory per conversation is not at least 2x better than L1 at 3,500 turns |
| L4 | Go core plus a small Rust byte kernel (Wasm SIMD128) owning the arena and doing scan, index, splice and hash | SIMD scanning and a smaller arena layout cut cold scan time without duplicating semantics | two toolchains, two linear memories, host-mediated calls | cold at 3,500 is not at least 1.3x better than the better of L1 and L3 |
| L5 | the slice in Rust (or Zig; one, chosen by the lane) with no Go | the ceiling for size, memory and cold on this design | duplicates semantics; not eligible as the product core | never; it is a measurement |
| L6 | TypeScript control: the same synchronous design, no Wasm, records as strings plus an index, V8 JSON | separates the design's win from the language's; V8 decodes JSON 3-6x faster and encodes 15-20x faster than Go in Wasm | not one codebase with PiG | never; it is a control |

Baselines, measured in the same sessions:

| id | target | why |
|---|---|---|
| L0 | empty target: a DO that answers `/wake` and `/turn` immediately | the harness floor (~8 ms per turn on the researcher's box); warm numbers are reported net of it |
| B1 | pi-durable 1.0.4 as published in durable-bench | the reference |
| B2 | pi-durable 1.0.4 patched to keep each conversation's derived context range across task invocations, extended by commits and invalidated by head markers and edits | the moving bar: upstream's unreleased fix reuses the scanned range within one invocation; B2 is stronger, so beating B2 beats the fix. The patch must keep `store` and `commit` identity with B1 and pass upstream's durable tests. The durable-bench-pig lane already built it as `pi-ctx` (`bench/durable/pi/patch-durable.mjs`, which patches `dist/harness/context.js`); S0 reuses it and applies the same change to `src/harness/context.ts` for the upstream test run, because those tests run on source |
| B3 | tardigrade 0.44.0 as published in durable-bench | the other contender. It cannot open a pi-durable store, so it runs only where durable-bench has a tardigrade target: W1, W2 (the bench-pig lane's `tardie` and `tardie-inc`) and W4b, on the fixtures durable-bench seeds for it from the same scripted transcript, whose seed fingerprints must equal those in section 4 |
| N1 | PiG native Go Durable (the durable-perf lane's branch), and L1's core built natively with the `sqlhost` adapter | informative only; not a DO number |

## 2. The slice (what every lane implements)

The bench slice is the durable-bench workload's semantics plus what makes a cold open real, marked "bench slice" in CONTRACT section 3:

- open: metadata, root conversation, live documents, live tasks and submissions, reconciliation (§5.1);
- input submission into an idle conversation with an empty inbox (§6); final boundary settlement;
- generation phases `prepare`, `request`, `tools` (§8.3), tool phases `call`, `execute` (§8.4), sequential rounds (§8.5), usage ledger (§8.6), live document with throttled partials (§8.2, §8.3) and tool output progress;
- context derivation, all nine rules of §2.1 (CONTRACT section 3.3), including head markers written by pi-durable compaction in W4a fixtures (lanes honour existing markers; they do not run compaction);
- preparation's `pi.system` planning (§7.4), with the `section` and `env` effects (ABI section 6) when the bench agent or `HarnessOptions` define them, and tool-argument conversion as pi-ai `validateToolArguments()` does it (CONTRACT section 5.6);
- recovery of an interrupted turn at any commit (§8.3, §8.4);
- cold open with and without the sidecar, both sidecar modes, and both validation paths (ADR D6);
- the context plane for model calls (ABI section 6), including the delta form.

| lane | scope |
|---|---|
| L1, L2, L3, L6 | the whole slice |
| L4 | the whole slice in Go; kernel scope is bytes only: scan, index, splice, hash |
| L5 | the whole slice |
| any lane, optional | the wire plane for W4f (ADR H-D11) |

Out of the slice and left to the production core: hooks, custom tasks, the transaction channel, forks, compaction runs, inbox queueing, sub-agents, reload, parallel rounds, retries and deferred polling.

## 3. Hypotheses tested across lanes

| id | question | test |
|---|---|---|
| H-cold | Which cold path is fastest for real workloads: no sidecar (rows from the head marker), index-only sidecar with lazy bytes, or snapshot sidecar? | L1 and L3 run all three on W1 and W4a; report M1 breakdown and M8 |
| H-mem | Does one Wasm instance per isolate serving many objects beat one instance per object? | L1 and L2 with K = 1, 8, 32 objects per isolate (M5) |
| H-plane | Does the delta form of `model_context` remove the host's per-call parse cost? | L1 with and without the delta form on W1 at 3,500 |
| H-wire | Does the wire plane beat the context plane with a real provider shape? | optional, W4f |
| H-B2 | How close does B2 come to pi-durable's 50-turn turn time at every size? | B2 on W1 at all sizes |

## 4. Workloads

| id | workload | sizes (turns of history) | notes |
|---|---|---|---|
| W1 | durable-bench as published: ten eight-tool measured turns; the scripted model walks the whole transcript on every call, in the host, on the JS context it receives | 50, 250, 1,000, 3,500 | Pi's stub costs 4.4-5.8 ms per call at 3,500 (spike); core lanes pay the same walk |
| W2 | durable-bench with the incremental scripted model (O(new messages) per call), in the host | same | must produce W1's fingerprints |
| W2c | the scripted model inside the core over its index (`bench_stub`) | same | reported in a separate, labelled table; never compared with B1-B3 |
| W3 | streaming: each response streams 40-60 chunks over 1-2 s; `partialIntervalMs` 100 | 50, 1,000 | wall clock in timing runs, virtual clock in acceptance runs |
| W4a | compaction-bounded history: pi-durable-written fixtures with compaction on and a 200k-token window; the measured turns run with the compaction policy disabled for every target, so no target compacts inside the timed region and the slice never needs to | 1,000, 3,500 | tests ADR H-D5: cold should be flat in history length |
| W4b | every turn cold: the runtime restarts before each measured turn | 50, 1,000, 3,500 | idle objects are evicted between user turns, so most real turns are cold |
| W4c | 32 KB tool results | 50, 1,000 | larger records through scan, splice and partial commits |
| W4f | recorded OpenAI Responses and Anthropic Messages streams served by a local mock | 50, 1,000 | wire-plane lanes only |

Every workload variant runs on every target that can run it, baselines included, and A1 compares a lane with B1 under the same variant. The model layer is the same JS code in the same position of every target's host: pi-ai's faux provider with its per-`sessionId` prompt cache, the scripted reply function of the variant, and the bench tool. The faux provider's usage values are stored in assistant entries, so a lane that bypassed it would also fail A1; its O(context) prompt-cache work (`serializeContext`, `commonPrefixLength`, about a quarter of B2's CPU per the bench-pig lane) is paid by every target alike.

Fixtures for pi-durable-format targets (B1, B2 and every lane) are pi-durable-written and shared (the bench-pig lane's `fx/pi-node-*.sqlite`, equal to durable-bench's published fixtures: 237,568 B at 50, 9,265,152 B at 3,500); B3 uses its own (section 1). Seed fingerprints: 50 `b017b487524e44a4`, 250 `dcea9f30b0917245`, 1,000 `ac520308146f2a8f`, 3,500 `0a8c8e4b0d9a0794`. W4a fixtures are new: S0 writes them once with pi-durable and the scripted model, and every target uses the same files. A sidecar-mode cold sample stages the fixture plus the sidecar the same lane build wrote in an untimed open-and-idle run; every lane also reports no-sidecar mode, and sidecar bytes count in M8.

## 5. Metrics

Every result line records all of these. Timing is taken from the Node side of Miniflare's `dispatchFetch`, as durable-bench does.

| id | metric | definition |
|---|---|---|
| M1 | cold | `cold-object`: the object is not in memory but the isolate is (K objects staged in one runtime, each woken once), from request to the end of the first measured turn; `cold-isolate`: a fresh runtime, the same span. Wasm compile, instantiation and runtime init are inside `cold-isolate`, and inside `cold-object` when the lane runs one instance per object; with one shared instance per isolate (H-mem) only the first object of an isolate pays them. Every result line records the instance policy; the primary score uses the policy the lane proposes for production, and H-mem lanes report both. L0's cold is reported next to every cold table as the platform floor and is never subtracted. Breakdown: compile (startup), instantiate, runtime init, open reads, sidecar load or scan, first turn |
| M2 | warm | median of measured turns 2-10 per sample, minus L0 measured in the same session; raw values are kept. Each line also records the model layer's own time per turn (scripted reply, faux provider, tool), measured in the host, so the Durable share is warm − L0 − model layer. W3 is also reported net of a stream floor: the same streams through the model layer with no Durable (the bench-pig lane's `stream-floor`) |
| M3 | size | Wasm raw, gzip and brotli; whole bundle (JS + Wasm) uncompressed against the 64 MiB limit |
| M4 | startup | Worker startup time (`wrangler` reports `startup_time_ms`; locally, Miniflare start to ready) against the 1 s limit |
| M5 | memory | Wasm linear memory after open, after the measured turns, and peak; JS heap delta; process RSS (durable-bench's measure); memory per conversation = (total with K objects − total with 0) / K for K = 1, 8, 32 |
| M6 | rows | `rowsRead` and `rowsWritten` per measured turn and per cold open (DO cursor counters summed by the host; baselines through an instrumented `sql` wrapper like `pix.ts`) |
| M7 | statements | SQL statements and commits per measured turn |
| M8 | storage | database bytes after the measured turns; sidecar bytes separately |
| M9 | CPU | CPU time of the runtime process per measured turn (`/proc/<pid>/stat` delta of workerd) |

## 6. Acceptance (numbers count only after these pass)

| id | gate |
|---|---|
| A1 | `store` and `commit` identity with B1 (CONTRACT section 2.1), with B1's restart points, on W1, W2 and W3 at every size of each, on W4a, W4b and W4c at their sizes, and on X1 (W1 at 50 with unknown fields injected), which is the bench-slice list of CONTRACT section 6 |
| A2 | Context fingerprints (`ctx` and `bench`, CONTRACT section 5.4) equal to B1 for every model call, both scripted-model variants |
| A3 | Handoff matrix (CONTRACT section 5.5) on W1 at 50 and 1,000 for every commit of one measured turn, at 3,500 for 10 sampled commits, and on W3 at 50 for every partial commit |
| A4 | Sidecar robustness: deleting, truncating or corrupting the sidecar, a pi-durable turn between two core turns, and a different `core_id` all yield the same stores and fingerprints as without a sidecar |
| A5 | Limits: no bound value above 2 MB, no statement above 100 KB, at most 100 parameters, sidecar chunks ≤ 1 MiB |
| A6 | ABI conformance (ABI section 12) for every Wasm lane; L6 runs the JSON form of the vectors |

## 7. Run rules

1. Quote only smc1 numbers. Sandbox and laptop numbers are ratios. smc1 is shared (load 15-60): gate each sample on quiet CPU (`CPU_MAX=0.25`) and record the gate reading and the 1-minute load average per sample.
2. Pin and record: Node, Miniflare 4.20260730.0, compatibility date 2026-07-30, `nodejs_compat`, Go 1.27.1, the TinyGo, Rust and Zig versions, pi-durable/pi-ai/chord 1.0.4, tardigrade 0.44.0, and each lane's commit.
3. Interleave targets within a session (round-robin per size), and run at least two sessions at different times of day. Report the median and p90 over all samples, with a bootstrap 95% interval.
4. Aggregate at least 100 measured warm turns per (target, workload, size): 12 samples of 10 turns, since M2 counts turns 2-10 (108 turns). Turn timing is taken in Node outside the runtime; in-isolate breakdowns (M1) read workerd's clock, which ticks in 1 ms, so single values below ~20 ms are not individually meaningful.
5. Cold: at least 100 `cold-object` samples (K staged objects per runtime start) and at least 20 `cold-isolate` samples per (target, workload, size).
6. Stage a fresh fixture copy per sample and read it once before timing (durable-bench `stage()`), so page cache state is equal.
7. Report net of L0 for warm; never subtract the floor from cold.
8. Results are JSON Lines under `bench/durable/do-core/results/`, one line per sample, carrying the M1-M9 fields, the workload, the scripted-model variant, fingerprints, acceptance flags, and host information. The report generator produces the tables in section 8 from those lines only.

## 8. Report and decision

Each lane reports, on its branch under `bench/durable/do-core/<lane>/`: its implementation summary (scope, lines of code, toolchain flags), the acceptance results, the M1-M9 tables against L0, B1, B2 and B3 for every workload and size, raw JSON Lines, and the exact commands.

Decision, applied by the lead:

1. A lane that fails any acceptance gate is out.
2. Primary score: `cold-object` p50 on W1 (full-walk variant) and W4a at 1,000 and 3,500 turns, and on W4b.
3. Secondary: memory per conversation at K = 32 and startup; then warm net of the floor.
4. The product core stays one Go codebase with PiG unless a non-Go lane beats the best Go lane by at least 1.5x on the primary score and 2x on memory; that result goes to the owner with the semantics-duplication cost, because the production core is far larger than the slice. L5 is not eligible; it bounds what the design can reach. If L6 wins outright, that also goes to the owner: it would mean Wasm costs more than it buys in a DO.
5. The chosen lane must beat B2 at every size and B3 on every workload B3 runs (W1, W2, W4b) on cold, and both on warm net of the floor from 250 turns up. If no lane does, the design is revisited before any production work.

## 9. Order

S0 first; it converts the wasm-spike lane's `src/engine.ts` and `core/` (ABI-free measurement code) into the ABI 1 shim and L1 seed, adds runner targets for every lane, and builds the slice subset of the contract tools and B2. L1 starts from the wasm-spike core as soon as the ABI 1 shim runs. L2, L3 and L6 start in parallel once L1 passes A1. L4 starts once L1 and L3 have cold breakdowns, because its hypothesis is relative to them. L5 runs whenever a lane is free. The wasm-spike and bench-pig lanes' existing results (scanner, splice, crash handoff, toolchain matrix, Miniflare baselines) are inputs, not repeated work.
