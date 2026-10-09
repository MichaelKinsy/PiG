# Final session runner spec: durable-repro × durable-report

Status: spec, agreed between lanes durable-report (renders) and durable-repro (runs). It extends durable-repro steps 1-2 (`bench/durable/repro/` on the durable-repro branch) to the production core and to step 4's realistic workloads. The output is one results file that `assemble.mjs` builds and `render.sh` turns into the deck.

## 0. Preconditions (owner rule)

1. The production PiG Durable core (lanes dcore-*, integrated on the dcore-integrate branch) has passed the CONTRACT gate: `store` and `commit` identity with pi-durable main `da866ada` on the full corpus plus the crash matrix (`docs/plan/durable-core/CONTRACT.md` sections 5.5 and 6), for each build that appears in the deck (TinyGo, Go, native).
2. The CONTRACT report is committed and has an https link. The gate is `make durable-contract` (`durable/contract/`, pinned to Pi main `da866ada` in `durable/contract/PIN`), run with `--strict` over the reference and every deck build. `meta.contract` is not typed by hand: it comes from that run's `report.json`:
   ```sh
   node contract-record.mjs --corpus durable/contract/corpus.toml --report <out>/report.json --core <C> --url <https link> \
     --build TinyGo=wasm-tinygo:<tinygo module> --build Go=wasm-go:<go module>
   ```
   It refuses unless `pi` and every listed build passed every `ready` row (the crash-matrix rows `H-*` and `X1-live` included), and records the sha256 of each module. Section 2 requires the measured modules to have those sha256 values.
3. Do not run PiG targets for the deck before 1 and 2 hold. No number from a benchmark-slice prototype is used, in any slide, in any status. `lib/results.mjs` enforces this: it rejects PiG lines without a green contract record, PiG versions other than the gated core, builds the gate did not run, modules whose sha256 differs from the gated one, and the prototype cores `872c81bf5` (wasm-spike, the step 2 `pigwasm-*` targets) and `407c624b7` (durable-perf).
4. pi-durable main and Tardigrade may be run before then, but the deck uses only the numbers from the one final session.

## 1. Pins

| item | value |
|---|---|
| host | smc1, Intel Xeon Platinum 8462Y+, `taskset -c 23` on bun and its workerd children; record `ENV.txt` as in step 1 |
| CPU gate | durable-bench's gate restricted to CPU 23 and its sibling 87 (`runner/gate-cpus.patch`), `CPU_MAX=0.08`, 1 s window, reading recorded per sample |
| runner | badlogic/durable-bench `e9abffa` + `runner/gate-cpus.patch` + `runner/pig-targets.patch` (section 2), applied in that order with `patch -p1`; nothing else changed. Commit both patches next to the results with their sha256 |
| toolchain | node 26.7.0, bun 1.4.0, miniflare 4.20260730.0, compatibility date 2026-07-30, `nodejs_compat`; Go 1.27.1; the TinyGo version the CONTRACT gate used |
| pi-durable main | pi checkout at `da866ada`, `PI_SOURCE=../pi PI_HEAD_VERSION=da866ada`, built from source by durable-bench's `pi-head` target |
| Tardigrade | `tardie` 0.44.0 from durable-bench's lockfile |
| PiG | the gated core commit `C`; modules built from `C` whose sha256 equals that of the modules the CONTRACT gate ran |

## 2. Targets

| durable-bench target | report key | role | notes |
|---|---|---|---|
| `pi-head` | `pi-head` | reference | pi-durable main with its own DO SQLite adapter |
| `tardie` | `tardie` | contender | as published in durable-bench |
| `pig-tinygo` | `pig-tinygo` | headline | production core, TinyGo build |
| `pig-go` | `pig-go` | detail | production core, Go build |
| `pig-native` | `pig-native` | detail | production core, native build (native process with its own SQLite, not a Durable Object; its lines get host `native`) |
| `ts-control` | `ts-control` | control | the gate's vendored TypeScript core (`impls/ts-core`), labelled "design control" on the builds slide only; optional |
| `empty` | `empty` | floor | `runner/floor.patch`: the same Worker, request and Durable Object path with no agent (`src/empty.ts`), measured in every round at every size, never seeded |

## 2a. Reporting rules

- **Rule A** numbers come from durable-bench's JS model on the same model path as pi-durable, in a Durable Object (Miniflare, or Cloudflare in section 5). Every headline number is Rule A.
- **Rule B** numbers come from the native build: its own scripted model in the core's harness. They are a harness floor and appear only on the builds slide, tagged Rule B.
- Every slide title carries the rule of its numbers; a slide that mixes rules tags each series in its legend.
- Latency is reported as p50, p95 and max, net of the floor. Warm pools every turn after the first of every fresh runtime: at least 100 per target and size (12 rounds give 108). Cold is open + first turn, one per runtime. The floor is the p50 of the `empty` target's same latency at the same size; Rule B has no request path and no floor. The final gate refuses a file without the floor target or with fewer than 100 warm turns in a headline, reference, contender or floor cell.
- SQLite rows written per warm turn and the Wasm memory high-water mark (`memory.buffer.byteLength` after every request, which never shrinks) are reported at every size, from the count probes of section 4a.

`runner/floor.patch` (sha256 `48ec978c93c93a2c7d8e57f842029f743b645f2d438843018b9f03781cfa68d2`) applies after `cloudflare.patch` with `patch -p1`. It adds `src/empty.ts` and the `empty` target, which stages an empty directory instead of a fixture.

`runner/pig-targets.patch` adds the targets `pig-tinygo` and `pig-go`. Their entry, `src/pig.ts`, installs the compiled core with the facade's `useCore` and re-exports durable-bench's `src/pi-head.ts` unchanged: the same Durable Object, scripted model, lookup tool and settings. The build resolves `@earendil-works/pi-durable` (and `/storage/sqlite/cloudflare`) to the core's pi-durable-compatible facade, `durable/core/host/cf/src/facade/index.ts`, and pi-ai and chord, for durable-bench's code and the facade alike, to the same Pi sources (`PI_SOURCE`) that pi-head is built from. One chord context and one faux provider are in the bundle, and they are pi-head's: the npm pi-ai 1.0.4 that the facade's package installs has a different faux provider (its `serializeContext` joins the whole transcript on every model call, about 3 MB at 3,500 turns), so resolving to it would charge the PiG rows for model work that pi-head does not do. The build refuses any module loaded from an npm copy of a Pi package (`node_modules/@earendil-works/`) for these targets. Each target's bundle sits in `dist/<target>/` beside its own copy of the core, loaded by Miniflare as a `CompiledWasm` module. Set `PIG_SOURCE` to a PiG checkout at the gated core commit `C` (with `make -C durable/core build-tinygo build-go` run and `npm ci` done in `durable/core/host/cf`) and `PIG_VERSION=C`. The modules in `$PIG_SOURCE/durable/core/host/cf/dist/` must have the sha256 values of `meta.contract.artifacts`.

Rules for the PiG targets:

- `version` in durable-bench's `TARGETS` is the core commit `C` for all three builds. The design control keeps its own version.
- The model is durable-bench's own scripted model in JS, running in the host on the context the core returns (step 2's `-js` variant), with pi-ai's faux provider and the same lookup tool. This is the headline variant and the only one in the deck. The core's internal stub is the bake-off's W2c, which is never compared with baselines.
- The sidecar mode is the production default. Each PiG target seeds its own fixtures (section 3), so the sidecar is written while seeding: its time counts in "building the history" and its bytes count in the database size.
- Record each module's raw bytes, gzip bytes and sha256 in `meta.targets[k].artifact`.

## 3. Session

One session, all targets interleaved. Run it in one `tmux` session on smc1. Keep its log and the raw durable-bench outputs.

`runner/run-session.sh` runs steps 1 to 5 below (its header lists the variables; `DRY=1` prints the commands). It seeds every durable-bench target, checks each seed fingerprint, measures all targets in rotated rounds with the native build in the same rotation (section "Native build"), seeds the largest size twice more at one third and two thirds of the rounds, runs the where-it-runs probe of section 4a, and writes `ENV.txt`, `seeds/`, `results.jsonl`, `native.jsonl`, `probe.jsonl` and `size.jsonl` to `$OUT`. It refuses to start when durable-bench's `results/results.jsonl`, `results/probe.jsonl` or `results/size.jsonl` already exists. `PI_HEAD_VERSION` must name the reference commit. Each step runs under `timeout` (`STEP_TIMEOUT`) and is tried up to three times; every retry is written to `ENV.txt`. `CLIENT=node` runs durable-bench's scripts on Node 24 instead of Bun (the 2026-10-08 session needed it: Bun's child-process spawn hung under host load). `RESUME=1` keeps the seeds already in `$OUT`.

1. Quiet check and `ENV.txt` (step 1 procedure). Record the start time.
2. **Seed, timed, from zero.** durable-bench's `bench/seed.ts` reports, for each size, the time since the previous size of the same invocation. A `50 250 1000 3500` ladder therefore reports the 1,000 → 3,500 increment as "3,500". This includes step 1's `seedMs` values (pi-head 39.8 s, tardie 299.6 s at 3,500), which are increments, not full builds; do not quote them as building time. The final session seeds each size in its own invocation:
   ```sh
   i=0; for n in 50 250 1000 3500; do i=$((i+1)); for t in $(rotate-by $i pi-head tardie pig-tinygo pig-go); do
     taskset -c 23 bun bench/seed.ts $t $n          # fixtures/$t-$n/ and fixtures/$t-$n.json (seedMs, fingerprint, bytes)
     cp fixtures/$t-$n.json seeds/$t-$n-run1.json
   done; done
   ```
   Alternatively, seed one `50 250 1000 3500` ladder per target and pass, and set `meta.protocol.seed` to `"chain"`. Each size's JSON of a ladder is a segment: before copying it, add where it started and the pass (`jq '. + {from: "empty", pass: 1}'` for 50, `{from: "50", pass: 1}` for 250, and so on), because a JSON without `from` counts as a from-scratch run. Every segment needs its fingerprint. The deck sums the contiguous segments of each pass (README, "building the history").

   Then seed 3,500 (or one more ladder) twice more per target at different points in the session, between measurement rounds, and keep each JSON (`seeds/$t-3500-run2.json`, `-run3`). The deck shows the median over runs. Every seed's fingerprint must equal durable-bench's (`b017b487524e44a4`, `dcea9f30b0917245`, `ac520308146f2a8f`, `0a8c8e4b0d9a0794`); stop the session if one differs.
3. **Measure, interleaved.** Twelve rounds. Each round runs every size, and every target within a size in a rotated order, one fresh runtime per invocation:
   ```sh
   for r in $(seq 0 11); do for n in 50 250 1000 3500; do for t in $(rotate-by $r <all bench targets>); do
     SAMPLES=1 taskset -c 23 bun bench/run.ts $t $n  # appends one line to results/results.jsonl
   done; done; done
   ```
   `rotate-by k list` is the list rotated left by k modulo its length, so no target always runs in the same position. That gives 12 samples per (target, size), 12 cold values and 108 warm turns, which meets BAKEOFF rule 4. `<all bench targets>` includes the realistic targets of section 4, so every comparison in the deck comes from the same rounds.
4. Copy `results/results.jsonl` and the seed JSON files out of the durable-bench checkout. Do not edit them.
5. Record the end time and the load range.

### Native build

durable-bench runs targets in workerd, so it cannot run the native build. The gate's native host, `durable/core/sqlhost/cmd/bench` (built by `go build -o durable/core/sqlhost/bin/bench ./durable/core/sqlhost/cmd/bench`, the binary whose sha256 `contract-record.mjs` records), runs the same scenario with a Go port of the scripted model (`workload.go`, a port of `durable/contract/lib/workload.mjs`). Its `--timing` mode (commit `cd3f2b05d`, until it merges into the integrated core) measures instead of tracing: the production host on the real clock, with no statement trace and no deterministic-time harness. Each run appends one `bench/run.ts`-shaped line, `{target, version, turns, sample, open, turn: [...], rss, bytes, tables, fingerprint, at}`. `open` is the time from opening the store to an open root conversation (run.ts `/wake`), `turn[i]` is each measured turn's wall time, `rss` is the process resident set in MiB, and `bytes` is `page_count * page_size` (SqlStorage `databaseSize`). It refuses a fixture whose seed fingerprint is not the published one for its size. For the session:

1. Build the binary from the gated core `C` (its sha256 is the one `contract-record.mjs` records for `native`).
2. Per sample, copy the standard fixture of that size to a fresh file and run, under `taskset -c 23`, in the same rounds as the other targets (section 3, step 3), one fresh process per sample:
   ```sh
   taskset -c 23 durable/core/sqlhost/bin/bench --db <fresh copy> --timing results/native.jsonl --version C --size <n> --sample <i> --turns 10 --tools 8
   ```
3. Pass `results/native.jsonl` to `assemble.mjs` with the other `--samples` files. Its lines get host `native`.

The native build has no "building the history" number: the deck shows it on the builds slide only.

Set `meta.targets["pig-native"].model` to the model it ran (for example "Go port of durable-bench's full-walk scripted model, in process"). A non-placeholder file refuses a native target without it, and the builds slide prints it: the native numbers then show the cost of WebAssembly and of the JS model together, not WebAssembly alone.

## 4. Realistic workloads (durable-repro step 4)

Use the variant agents of `bench/durable/fixtures/pi-head-x.ts` (durable-bench-pig `b3af4572e`) and the same variant for the PiG targets:

| workload | bench targets | sizes | what it adds |
|---|---|---|---|
| `compact-big` | `pi-head-compact-big`, `pig-tinygo-compact-big` | 50, 250, 1,000, 3,500 | compaction on (131,072-token window) and 4-64 KiB tool results: the headline realistic slide |
| `big` | `pi-head-big`, `pig-tinygo-big` | same | multi-KB tool results, no compaction |
| `compact` | `pi-head-compact`, `pig-tinygo-compact` | same | compaction on, standard tool results |

- Keep the fixture metadata on pi-head's run-session lines (`fixture.meta`: entries, entry bytes, compactions, active entries). `assemble.mjs` copies it into `meta.workloads.<w>.fixtures`, and the slides print entries, transcript bytes and compactions under every size.
- Each target seeds its own variant histories from zero, as in section 3, step 2. The fingerprints must equal the fixture table in `bench/durable/fixtures/README.md` (also in `lib/results.mjs`). If the variant agent changes, update both together.
- Compaction stays on in the measured turns, as seeded, so a measured turn can compact. Both systems write the same rows (CONTRACT gate), so they do the same compaction work.
- Tardigrade has no equivalent stores. It is left out of these slides, and each slide says so.
- `pig-go-*` and `pig-native-*` variants are optional. Each adds 48 runs to the session.

## 4a. Where TinyGo runs (owner question: are we at any disadvantage to pi-durable on a Durable Object?)

`runner/where-it-runs.patch` (sha256 `213416331e288671dfc70e96032d2bd7a2e0415794b6c22072cae1954f2ad120`) applies after the two section 2 patches with `patch -p1`. It adds a probe that is compiled in only when `BENCH_PROBE` is set, into a separate `dist/probe-<mode>/` bundle, so the latency runs of section 3 are unchanged. pi-head and the PiG targets run the same Durable Object class; the probe measures both the same way:

| metric | how | limit or billing rule (Cloudflare docs, read 2026-10-08) |
|---|---|---|
| cold start | `open` (wake: module instantiate inside `Harness.open`, counted separately as `instantiateMs`) + first turn, fresh runtime | none; startup (global scope) must finish within 1 s |
| warm turn | turns 2-10; the slide's latency rows are the section 3 p50, net of the floor | none |
| CPU per request | sum of `/proc/<workerd>/task/*/schedstat` run time around each request | 10 ms Free, 30 s default Paid, per HTTP request. A Durable Object is billed by wall-clock duration (GB-s at 128 MB), requests, and SQLite rows; CPU ms bills the Worker. |
| SQLite rows read and written per turn | the cursors' `rowsRead`/`rowsWritten` | the Durable Object's billed storage metric |
| retained memory | after the last turn, a full collection (inspector `HeapProfiler.takeHeapSnapshot`; workerd does not answer `collectGarbage`), then `Runtime.getHeapUsage` `usedSize` + `backingStorageSize` + Wasm `memory.buffer.byteLength` (line field `retained`). The slide judges this. The same sum read after every request, before any collection, is shown in brackets: it counts garbage (2026-10-08: about 370 MB of uncollected JS and 300 MB of ArrayBuffers at 3,500 turns, against about 8 MB retained). Wasm memory never shrinks, so its value is exact either way. | 128 MB per isolate, JS heap and Wasm included |
| bundle size | `bench/size.ts`: bundle plus Wasm module, uncompressed, with gzip for reference | 64 MiB uncompressed. Cloudflare has no compressed limit; the deck says so. |
| JS<->Wasm crossings per turn | every export and import call is counted; `hopSteps` counts step calls delivering `hopped` (the crossings the hop-chain model adds) | none |
| time inside the core per turn | `BENCH_PROBE=time` only: performance.now around export calls. Miniflare's timer has 1 ms resolution, so the per-turn sum is an unbiased estimate; Cloudflare freezes timers during a request, so this row is Miniflare-only. | none |

`runner/run-session.sh` runs these after its latency rounds (`PROBE_ROUNDS`, default 3; `PROBE_SIZES`, default "50 3500"), in the same session and with the same pins, rotating targets per round:

```sh
for t in pi-head pig-tinygo pig-go; do bun bench/size.ts $t; done                       # results/size.jsonl
for r in 0 1 2; do for n in 50 3500; do for t in $(rotate-by $r pi-head pig-tinygo pig-go); do
  BENCH_PROBE=count SAMPLES=1 taskset -c 23 bun bench/probe.ts $t $n                     # results/probe.jsonl
  BENCH_PROBE=time  SAMPLES=1 taskset -c 23 bun bench/probe.ts $t $n
done; done; done
```

On Cloudflare (section 5) the same probe bundle reports counts, rows and Wasm memory through `/probe`; CPU time comes from Cloudflare's invocation records (`wrangler tail`), and wall time from the client. The slide flags every metric where PiG Durable is worse than pi-durable.

## 4b. Tracking one row between sessions

`runner/track-row.sh <core commit>` re-measures PiG Durable (TinyGo) at 3,500 turns on one core commit. It builds the TinyGo module and seeds both PiG TinyGo and pi-durable main from zero, with the fingerprints checked. It runs 12 rotated rounds of PiG TinyGo, pi-durable main and the empty-target floor, then one count probe each of PiG TinyGo and pi-durable main with retained memory. It reports as the deck does (section 2a, Rule A). Every PiG metric has its pi-durable value from the same run: warm and cold p50 and p95, CPU per warm turn, cold-start CPU, retained memory, seed time, rows read and written. pi-durable's own numbers drift on a shared host (warm p50 267-390 ms across runs on 2026-10-08), so each PiG metric is also judged against the rolling median of the pi values of the last 5 runs, this one included (`HISTORY`). `disagree` lists the metrics where the two verdicts differ on win or lose, and the one-line `summary` says so. Verdicts use the deck's rule: a loss when PiG is more than 3% worse, a win from 1.3×. These numbers are a private tracker between dcore milestones, not deck numbers.

## 5. Cloudflare (only if the owner's account is ready)

This depends on RISKS-AND-QUESTIONS Q1. Without it, the deck states "Local workerd (Miniflare) only". Nothing in this section runs without `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` for the owner's Workers Paid account.

`runner/cloudflare.patch` (sha256 `f52679c45dba575a93c8fcfba1a34d1c2b9a5246ee2fe82c53077734cb4803a1`) applies after `where-it-runs.patch` with `patch -p1`. It adds `?o=<name>` to `src/serve.ts`, so one deployment serves one Durable Object per sample, and `bench/cloud.ts`:

```sh
# Upload the bundles Miniflare runs, byte for byte (no_bundle; wrangler 4.116.0 ships the pinned workerd).
for t in pi-head pig-tinygo; do
  bun bench/cloud.ts deploy $t                         # Worker durable-bench-<t>; URL + sha256 -> results/cloudflare-deploy.jsonl
  BENCH_PROBE=count bun bench/cloud.ts deploy $t       # Worker durable-bench-<t>-probe-count
done
# Latency: one object per size and sample, seeded through /seed (fingerprint checked against the local fixture),
# then measured after the eviction window (IDLE_MS, default 150 s).
for r in 0 1 2; do for t in $(rotate-by $r pi-head pig-tinygo); do
  SAMPLES=1 bun bench/cloud.ts run $t <url of durable-bench-$t> 50 3500                                    # results/cloudflare.jsonl
  BENCH_PROBE=count SAMPLES=1 bun bench/cloud.ts run $t <url of durable-bench-$t-probe-count> 50 3500      # results/cloudflare-probe.jsonl
done; done
```

- Record the account plan, the colo that served the objects, and the client location in `meta.cloudflare.label`, and set `meta.cloudflare.status` to `measured`.
- Cold: open and first turn of an object idle longer than the eviction window. Warm: turns 2-10. Each request is timed at the client; `floor` is the median Worker-only round trip (`/setup`, which does not reach the object) of the same sample.
- CPU time per request: `wrangler tail --format json` during the probe run. Requests run one at a time, so a `/wake` or `/turn` Worker invocation plus the Durable Object invocations up to the next Worker invocation make one request. The run refuses to write lines with a request missing, and keeps the raw tail in `results/cloudflare-tail-<worker>-<run>.jsonl`. On the first live run, check that the tail events carry `cpuTime` and `executionModel` as read here.
- Cloudflare does not expose the isolate's JS heap: Cloudflare probe lines carry `heapUsed`, `heapTotal` and `backing` as `null`, and the slide shows the core's Wasm memory only, without a verdict. Timers are frozen during a request, so only count probes run there.
- Pass `results/cloudflare.jsonl` to `assemble.mjs --cloudflare` and `results/cloudflare-probe.jsonl` to `--probes`. The deck shows these numbers on their own slides ("Where TinyGo runs, on Cloudflare" and the Cloudflare latency slide) and does not compare them with smc1 numbers.
- Delete the Workers afterwards (`npx wrangler@4.116.0 delete --name <worker>`).

## 5a. PiHarness bake-off (performance only)

`runner/piharness-targets.patch` (sha256 `b29321563a17ea9b4dd3935156b611af73e8863d8d6e8b0016ce433291045eb3`) applies after `floor.patch` with `patch -p1`. It adds the targets `piharness-pi` and `piharness-pig`, which run pi-head's agent, scripted model, tool and settings inside the Cloudflare Agents SDK's `PiHarness` (`agents` 0.28.0, `agents/harness/pi`, beta) and its `Lifecycle`. Both targets use the SDK's Durable Object SQLite adapter's table names (every Pi table and index renamed with the `pi_` prefix) and its wake (one Lifecycle job per session, rescheduled every 30 s while tasks run). Their `/stats` refuses a store with a table outside the `pi_` prefix (other than the SDK's `cf_` tables), so a seed or sample that ran outside the adapter's names fails. A turn is `harness.submit` and `harness.wait` with the turn id as the operation id, then the root conversation's `waitForIdle`, as in pi-head. `piharness-pi` resolves pi-durable, pi-ai and chord to the Pi sources (`PI_SOURCE`), as pi-head does. `piharness-pig` resolves them as `pig-tinygo` does, and resolves `@earendil-works/pi-durable/storage/sqlite`, which `PiHarness` opens its store with, to `src/pig-sqlite.ts`: the PiG store over the same adapter.

The PiG core runs synchronous SQL, and the SDK's adapter is asynchronous. The bench therefore forks the adapter at build time (esbuild `onLoad`, refused unless `agents/dist/harness/pi/index.js` has agents 0.28.0's sha256 `5f2b329b2c5b4da97ad0e61e68267a02a5a5683b12895eabfbdfbbee705e9d73`): `DurableObjectSqliteDatabase` gains `syncStorage()`, a `ctx.storage`-shaped handle whose statements pass through the adapter's own `Prefixer`. Pi's statements take the unchanged path. The adapter runs a statement at once when no transaction is open, so it has fewer microtask hops than Pi's own `SerialQueue` adapter. PiG's statements skip the adapter's `OperationQueue` and its bind and row conversions, as `pig-tinygo` skips Pi's `SerialQueue` adapter. These rows compare performance only; ordering parity is judged on Pi's own adapter (section 2), not here.

durable-bench's `bun.lock` does not list `agents`. Install it and the one dependency the bundle takes from it, `nanoid`, from their npm tarballs, whose sha1 values are npm's `dist.shasum`:

```sh
npm pack agents@0.28.0 nanoid@5.1.16   # sha1 96d07545264c608b50fa2d6abebeedd39d9f83e5, fe345c0a1f9007c32fbb5c139e1208bfd3f41ef7
for p in agents-0.28.0 nanoid-5.1.16; do d=node_modules/${p%-*}; mkdir -p $d && tar -xzf $p.tgz -C $d --strip-components=1; done
```

The bundle imports nothing else from agents' dependencies. Without `agents` installed, the other targets still build and run.

Before PiG can be measured here, its core must reopen a store under the `pi_` prefix. PiG detects its schema with a probe that names `durable_schema` and `pig_sidecar` inside string literals, which the `Prefixer` does not rewrite. The first open succeeds; every reopen re-runs the schema and fails (`table pi_durable_metadata already exists`). Every latency sample is a reopen, so `piharness-pig` has no row until the core detects its schema by identifiers, as Pi does.

## 6. Assemble and render

```sh
cd bench/durable/report
cp data/session-meta.template.json /tmp/session-meta.json    # fill every TODO; status "final"
node assemble.mjs --meta /tmp/session-meta.json --out data/results.jsonl \
  --samples <session>/results.jsonl --seeds <session>/seeds/*.json \
  --probes <session>/probe.jsonl --sizes <session>/size.jsonl [--cloudflare <session>/cf.jsonl]
./render.sh                                                    # data/results.jsonl -> final/png, final/tables.md
```

A final file with PiG lines must hold probe samples (count mode for PiG TinyGo and pi-head at every size, time mode for PiG TinyGo at 50 and 3,500 turns) and both upload sizes; the "Where TinyGo runs" slide flags every metric where PiG loses. `--samples` takes durable-bench `bench/run.ts` lines or durable-repro `run-session.ts` lines; `--seeds` takes durable-bench fixture JSON files or durable-repro `seed-time.ts` lines. `assemble.mjs` writes nothing if a gate fails. It records the sha256 of every input in `meta.sources`. The committed `data/results.jsonl` is the one source of every slide. `final/tables.md` gives the rule, the floor, n, p50, p95, max and a bootstrap 95% interval of the p50 for every cell.

Before review, check `final/tables.md` by hand:

- Compare pi-head in the first six rounds with pi-head in the last six. If the medians differ by more than 15%, say so on the method slide (`meta.text`) or rerun.
- Within-session differences below about 1.3× are within step 2's drift. Do not describe them as wins.

## 7. durable-repro's session of 2026-10-07 (`session-20261007T043133Z`)

`assemble.mjs` reads that session's `session-runs.jsonl` and `seed-time.jsonl` as they are. In `meta.bench_targets`, map `empty` and every prototype target (`pigwasm-*`, `pigts-*`, `pi-head-prof-inc`) to `null`, which drops them before the results file exists. A trial assembly of its pi-head and Tardigrade lines rendered cleanly as a local draft. That trial was not committed and is not for quoting. Five points must change before the production session:

1. **Loop order.** That session ran the sizes one after another and alternated the targets only within a size. Host load fell during the run, so pi-head's cold time at 3,500 turns (median 301 ms) came out below its time at 2,000 (344 ms). Section 3 puts rounds outermost and sizes inside each round, so every size sees the whole session's load.
2. **Seeding from zero.** `seed-time.jsonl` times the 50 → 250 and 50 → 1,000 increments, plus empty → 250 for pi-head and Tardigrade only, because the prototype cores cannot create a store. The deck's "building the history" uses only runs from an empty store (`from: "empty"`), at every size up to 3,500, for every contestant. The production core must create its own store.
3. **Fingerprints on seed runs.** A from-scratch seed run needs the context fingerprint of the history it built (`fingerprint` on the line or on each sample). Equal table counts are not enough. Without it, `assemble.mjs` stops at "fingerprint required for a from-scratch seed run".
4. **Fixture metadata per target.** `run-session.ts` (`fixtureInfo`) writes pi-head's fixture metadata into every Tardigrade line. Each line should carry the metadata and fingerprint of the fixture that target actually opened.

5. **Uninstrumented reference.** Step 4 (`results/step4/step4-runs.jsonl`) measured pi-durable main only as `pi-head-prof-full`, a profiled build. The deck's reference is plain `pi-head`. Run it without profiling in every workload, or give every target the same instrumentation and say so on the method slide. A trial assembly of step 4's pi-durable lines (with the profiled build standing in for `pi-head`) rendered the `big`, `compact` and `compact-big` slides cleanly. That trial was local only and is not for quoting.

## 8. Hand-off

durable-repro commits the raw outputs and patches under `bench/durable/repro/final/` and sends the paths. durable-report assembles, renders, reviews, and commits `data/results.jsonl` and `final/`. Nobody posts anything. The owner decides about publication (RISKS-AND-QUESTIONS Q7).
