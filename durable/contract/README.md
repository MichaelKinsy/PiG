# Durable core conformance gate

The gate every lane of the Durable core uses ([CONTRACT.md](../../docs/plan/durable-core/CONTRACT.md) sections 5-7). It runs the corpus (E00-E31, U, SC, I, W1-W4, X1, H) against pi-durable at the pinned reference commit (`PIN`, Pi 1.2.0 `06a46693`, which writes task `startedAt`/`endedAt` and message `durationMs`) and against a candidate, and compares the two at the store, commit, context, output and order levels (order: the scenario's API calls and the commits in one sequence, CONTRACT 5.8). One Pi commit is one row of `commit` lines, so a difference names the first divergent commit seq, the table, the column and whether the text differs in value, key order or number format.

## Run it

```sh
make durable-contract-setup                 # clone Pi at the pin into durable/contract/.cache, npm ci (needs git, npm, Node >= 24.19)
make durable-contract-test                  # the tooling's own tests, negative controls included (about 1 minute)
make durable-contract                       # the gate: pi (control), wasm-tinygo, wasm-go, native (E/U/SC through the JS host, W/X1/H through the bench host), native-bench (the bench-slice reference engine); a per-lane table
make durable-contract DURABLE_CONTRACT_ARGS="--impls ts --lane dcore-loop"
```

Direct use (`CONTRACT_PI_SOURCE` and `DURABLE_CONTRACT_CACHE` override the cache locations):

```sh
node cli/contract.mjs corpus --check                   # manifest valid, every source hashes to the pin
node cli/contract.mjs fixtures 50 250 1000 3500        # W1 fixtures, written by pi-durable; seed fingerprints must equal the published ones
node cli/contract.mjs run 'E*' W1-50 H-50 --impl ts    # rows (ids may end in *) against one implementation
node cli/contract.mjs run --impl pi                    # the control: the reference against itself (determinism gate)
node cli/rowdiff.mjs a.trace b.trace --level commit    # compare two traces
```

A row is `pass`, `fail`, `pending` (the implementation cannot run it yet, with the reason) or `skipped` (nobody can). `pending` never counts as `pass`; `--strict` makes it fail the run. A reference that does not run fails its row, a failed build stops the run, and a U file whose every test the reference skips is `skipped`, not `pass`. A failed row is reported against the lane that owns it (`owner` in `corpus.toml`): `dcore-session`, `dcore-loop`, `dcore-store`, `dcore-host`, `do-core-design`. The lanes are the packages of docs/plan/durable-core/LAYOUT.md (`dcore-store` owns the records, the store and the compaction and fork rows).

## How a capture works

| piece | file | what it does |
|---|---|---|
| ESM hooks | `hooks/resolve.mjs`, `hooks/register.mjs` | `node --import hooks/register.mjs <scenario>` runs a scenario from the reference's sources. The resolve hook maps `@earendil-works/*` to `src/`, swaps `MemoryStorage`, `openNodeJsonlStorage` and `openNodeSqliteStorage` for capsql-backed storage, wraps the model registry and `uuidv7()` for the tape, and aliases a candidate's modules over the reference's |
| capsql | `lib/capsql.mjs` | a `node:sqlite` database that records every statement and, per committed transaction, the row changes (a SQLite session decoded per table, sorted by table and primary key so statement order cannot change a commit). `pig_*`, `_cf_*` and `sqlite_*` tables are excluded. A commit's `seq` is the `next_seq` it consumed. `CONTRACT_CRASH_AFTER=<seq>` kills the process with SIGKILL right after that commit |
| detenv | `lib/detenv.mjs` | virtual `Date`, `performance.now`, `setTimeout`/`setInterval` (a timer fires only when the microtask queue is empty and no file, child-process or socket operation is pending), seeded `Math.random`, `crypto.getRandomValues`, `crypto.randomUUID`, deterministic `mkdtemp` suffixes. Normalisers are not allowed |
| tape | `lib/tape.mjs` | model calls (`streamSimple`, `completeSimple`, `fetchDeferred`, `cancelDeferred`) and every `uuidv7()` value. Record mode stores request fingerprint, events with virtual times and the result; replay mode serves them and a request that differs from the recording is a `mismatch` line, which the comparison reports as a context failure |
| rowdiff | `lib/rowdiff.mjs`, `cli/rowdiff.mjs` | `store`, `commit`, `context`, `output` comparisons with classified column differences |
| matrix | `lib/matrix.mjs` | the crash handoff matrix: control `pi>pi`, `P>X`, `X>P`, `X>X` at every commit of a measured turn, plus the clean-restart row `end`. Real processes, SIGKILL, the WAL kept |
| inject | `lib/inject.mjs` | unknown-field injection (X1): entry, `pi.usage` and `pi.live` bases at rest; live task, state, checkpoint, input and submission at the crash |
| gate | `lib/gate.mjs`, `cli/contract.mjs` | runs rows, memoises the reference captures, summarises per lane |

Every scenario runs in its own process with `HOME` and `TMPDIR` set to one fixed directory per scenario (`<tmp>/pig-contract/<id>`, serialised by a lock), so a path a scenario stores is the same for every implementation. A run always starts from an empty store directory.

### Trace format

JSON Lines (CONTRACT 7.6): `meta`, `store` (path of each store opened), `sql` (statement text by index), `commit` (`store`, `seq`, `stmts` with `q`/`k`/`p`, `reads`, `changes` with `table`/`op`/`pk`/`before`/`after`), `model` (`call`, `model`, `ctx` sha256 of the pi-ai Context and options, optional `bench`, `messages`), `effect`, `uuid`, `mismatch`, `final` (per-table digests of each store), `out` (stdout lines), `exit`, `end`. Bench values: `bench` is durable-bench's 8-byte fingerprint of `[role, text, calls]`.

## Rows

`corpus.toml` is generated by `tools/gen-corpus.mjs` from the checkout (sha256 of every source at the pin). Regenerate after a pin change and read the `corpus --check` diff.

| rows | runner | what is compared |
|---|---|---|
| E00-E31 | `example` | the upstream example runs under the hooks; store, commit, context, output, order. E16 needs a provider key and is skipped |
| U-* (49) | `vitest` | the upstream test files, run from source; a row passes when every test the reference passes passes, the reference running the same file at the same time (a test both fail under the load of the moment is named, not counted) |
| SC | `vitest` | `registerStorageConformance` over the candidate's write encoder (pending until the encoder exists) |
| I | `interop` | `durable/interop/scenario.mjs` under the hooks |
| W1-*, W2-* | `bench` | `scenarios/bench.mjs`: ten 8-tool turns on a copy of the fixture, full-walk and incremental scripted model |
| W3-*, W4b-d, W4g, X1 | `bench` | streaming, restart per turn, bounded 32 KiB tool results, parallel round, retryable error, unknown fields |
| W4a, W4e, W4f | `bench`, `cassette` | pending or skipped, with the reason in `corpus.toml` |
| F-gate-N, F-node-N | `diff` | `scenarios/diff-ordering.mjs --seed N`: a random schedule of parallel tools, hooks, observers and inbox arrivals; every level incl. order, on the `gate` or `node` storage profile (CONTRACT 5.9). `contract soak --seeds A-B [--shrink] --impl X` runs more seeds |
| T1-250, T1-3500 | `diff` | `scenarios/time-travel.mjs` on a copy of the `pi-main-N` fixture (`--db`): as-of context and agent document at turn h100, eight scrubbed as-of contexts, a fork there with one turn; store, commit, context and output levels (CONTRACT 5.9) |
| H-*, X1-live | `matrix` | the handoff matrix (sampled crash points for 3,500 turns) |

### Known differential failures (`baseline.toml`)

The F rows that differ from Pi today are listed in `baseline.toml` with owner, cause and a repro command. `contract run`, `contract all` and `contract soak` print them as FAIL and as `known failures`, and exit 0 for them. A failing row that is not listed exits 1 (`NEW FAILURES`), and a listed row that passes on every build exits 1 in `contract all` (`FIXED: delete them from baseline.toml`). Only F rows may be listed. `corpus --check` validates the file.

## Writing a candidate

`impls.toml` lists the builds the gate knows. A candidate provides either or both of:

1. **`candidate_dir`**: a directory laid out like `packages/durable/src` (the JS API of ADR D12). Every module it has replaces the reference's module of the same path; the rest come from the reference. This runs E, U, SC and I rows. Its storage modules open stores through `globalThis[Symbol.for("pig.contract")]`: `openStore(path)` returns a host-loop store (`run`, `all`, one `tx(work)` per commit), `openDatabase(path)` returns pi-durable's `SqliteDatabase` facade. Both write through capsql, so the candidate cannot write a row the contract does not see.
2. **`bench`**: a Node module exporting `runBench(options)` that runs the bench scenario and returns what `lib/run.mjs` `runScenario` returns (`trace`, `stores`, `code`, `signal`, `stdout`, `stderr`). The options are in `lib/impl.mjs`. `contexts: ["bench"]` limits the context comparison to the bench fingerprint for a core that does not build the full pi-ai Context. Scenarios outside a core's slice exit with status 3 and are reported as a failed run, never as a pass.

`ts` (`impls/ts-driver.mjs`, the vendored durable-core-ts control, `impls/ts-core/`) is the reference candidate: it passes W1, W2, W4c, X1, the H-50 matrix (77 crash points, 3 orders, plus the clean restart) and X1-live against Pi main. `test/matrix.test.mjs` breaks it four ways (key order, a missing `endedAt`, a usage total, recovery that does not requeue a task) and requires the matrix to fail naming the cause.

## What is not done

See `handoffs/do-core-design.md`.

## Fuzzing rule

Go fuzz tests of the byte-level differential suite (CONTRACT 5.6) always run bounded: `GOMAXPROCS=8 go test -fuzz=<name> -parallel=4 -fuzztime=30s <package>`. An unbounded `-fuzz` starts one worker per CPU and floods the shared build host. The Node side of this gate runs at most `-j` (default 8) scenario processes at once.

## R rows: stores written by Pi 1.0.4

PIN `release` names v1.0.4. `make durable-contract-setup` adds a worktree of it (`CONTRACT_PI_104_SOURCE`, default `.cache/pi-1.0.4`, sharing the reference's dependencies). Fixture kind `v1.0.4-<n>` is the W1 seed written by that source: its tasks have no `startedAt` and half its entries have no `durationMs`. `R-104-50` runs the bench scenario on it and `H-R-104` crashes after every commit of a measured turn on it; main and the candidate must read the old rows, write main's shapes for new ones, and leave the old rows alone. Passed by `pi` and `ts`.

## Claiming a row in the core's gate

`durable/core/contract/gate.sh` (the integrator's) runs the command of every `claimed` row of `durable/core/contract/corpus.tsv`. A row's command is this tool with a candidate:

```
node durable/contract/cli/contract.mjs run W1-50 --impl wasm-tinygo --strict     # the TinyGo build in the JS host
node durable/contract/cli/contract.mjs run W1-50 --impl wasm-go --strict         # the Go wasip1 build
node durable/contract/cli/contract.mjs run W1-50 --impl native --strict          # native Go, statements replayed through capsql
node durable/contract/cli/contract.mjs run W1-50 --impl native-bench --strict    # the bench-slice engine on the production store, rec and history packages
node durable/contract/cli/contract.mjs run SC --impl native-bench --strict         # the core's write encoder as a Storage under the upstream storage suite (impls/sc-native-provider.ts)
```

`--strict` makes a `pending` row fail, so a lane cannot claim a row the candidate cannot run yet. The command builds the candidate first (a failed build exits non-zero), passes `DCORE_*` and `DURABLE_CORE_*` variables (gate.sh's `DCORE_WASM_GO` and `DCORE_WASM_TINYGO`) through to the run, and exits 1 on any `fail`. Corpus ids are the ids of `corpus.toml` (`W1-50`, `H-50`, `E00`...); `corpus.tsv`'s coarse ids (`W2`, `W4a`, `H-W1`) are the ids with their size suffix.

A JS facade (`candidate_dir`) needs no bench module: the bench scenario program runs through it like an example. It provides `index.ts` and the modules the examples and the bench import by path (`env/node.ts`, `tools/index.ts`, `storage/sqlite/node.ts`, `storage/jsonl/node.ts`); a module the candidate lacks resolves to the reference's. Its storage opens stores through `globalThis[Symbol.for("pig.contract")]` (`openStore(path)`, `openDatabase(path)`) so every commit reaches the capture.

A facade candidate also names `wasm` (the core module its build produces, passed to the run as `DCORE_WASM`) and `setup` (a module of the facade, imported before the scenario program, that loads the core: `useCore(await WebAssembly.compile(readFileSync(process.env.DCORE_WASM)))`). A row stays `pending` while either file is missing; a facade that cannot find a core is not a failing core.

## Real-time tests (`real_time_tests`)

A U row may name tests whose assertion depends on a real-time window the reference shares, for example a test that sleeps 20 ms inside the 100 ms output throttle window. A process stall longer than the window fails such a test in the reference as well as in the candidate. When the candidate fails only tests named in `real_time_tests`, the row reruns the file once and passes only if those tests pass the second time. The row's detail then says `retried once`. A second failure fails the row. Add a test to the list only after measuring that the reference fails it at the same stall.

## Count budgets (`cli/counts.mjs`)

What a turn costs in work that does not depend on the machine, compared exactly with a committed baseline (`counts.json`). Build the cores first, as for the robustness proofs.

```sh
node cli/counts.mjs check    # every merge: exits 1 when a number differs from counts.json
node cli/counts.mjs update   # write counts.json from this build (commit it with the change that moved the numbers)
node cli/counts.mjs run --impls wasm-tinygo,pi --sizes 3500   # print only; `pi` adds the reference's store counts
```

Each build (wasm-tinygo, wasm-go and native by default) opens a copy of the W1 fixture at 50, 1,000 and 3,500 turns and runs 6 measured turns of the incremental scripted model (8 lookups a turn). With `CONTRACT_COUNTS` set, the hooks count every statement capsql runs and the rows it returns (`reads`, `rowsRead`), every write statement (`writes`), the row changes each commit makes to Pi's tables (`rowsWritten`, `commits`) and every call across the core boundary (`crossings`: export and import calls of a Wasm instance that imports `pig`, messages to and from a native core). When the core has `alloc_stats`, the runtime's `allocBytes`, `mallocs` and completed collections (`gcs`) are counted too. The native build also has `ir`: the native core process runs under valgrind's cachegrind (instruction counting only) over its whole life per cell (start, open, every measured turn and the final statistics), and `ir` counts the instructions of every source file outside the packages whose work follows the clock and the host's timing (`IR_CLOCKED` in `lib/counts.mjs`: the runtime, the poller, the fifo I/O, timers, locks, the runtime's random source and the gate's guard, whose watcher ticks every 100 ms). The Go runtime runs with `GODEBUG=asyncpreemptoff=1,gcstoptheworld=1` there, because preemption signals and background mark work follow the clock. Even so, the whole-process total moved by 3.2% over 15 runs at 1,000 turns and followed wall time; `ir` moved by at most 0.06% over 33 runs, so it has 0.25% slack. Allocation and collection work, which `ir` does not see, is gated by `allocBytes`, `mallocs` and `gcs`. `ir` and the replay's native `instructions` (below) use one valgrind: build the pinned release with `sh durable/contract/tools/build-valgrind.sh PREFIX` (user space, sha256-checked) and pass `VALGRIND=PREFIX/bin/valgrind`. Without `VALGRIND` a run with the native build refuses to start; it never falls back to a valgrind on `PATH`. `--ir 0` skips `ir`, so it suits `run` only: `check` and `update` need `ir`, and outside valgrind the native `heapNet` reads 300 to 430 B (0.12 to 0.17%) below the baseline measured under it. The baseline names the valgrind release, and a check with another release refuses to compare. `cold.*` covers the open and the first measured turn and `warm.*` the five others; `heap` is the core's heap after a full collection at the end (`mem_stats` slot 6; corenative collects twice, so nothing only a `sync.Pool` holds counts) and `linear` the Wasm linear memory's high-water mark.

The deterministic environment makes a run repeat exactly, so the check is exact, with one exception: the native core's runtime is not deterministic (it collects on its own threads, and map hash seeds, map growth and GC-timed allocations vary by process), so its gated heap (`heapNet`, below) may move by 0.25%, its `allocBytes` by 0.5%, its `mallocs` by 0.25% and its `gcs` by 25% (one collection in the four warm ones, none cold). Those slacks are a floor in `lib/counts.mjs` (`DEFAULT_TOLERANCE`); the baseline file may widen them, not narrow them. The Wasm builds stay exact. The native heap is read after two closing collections. About one run in eight under CPU contention reads 5,368 B (2.01% at 1,000 turns) more, because the Go scheduler started one more OS thread, and the runtime keeps a fixed set of heap objects for each thread it has started. So a native cell gates `heapNet`, the heap minus the runtime's live threads (`/sched/threads/total`) times the heap one thread holds, which the same process measures at its first `heap_threads` request (op 10 of `cmd/corenative`): it starts six more threads one at a time and takes the median heap the last five added each, after two collections (5,320 B on go1.27.2; the first thread also pays for the runtime's once-only state, and one in a few holds a 48 B object of the scheduler's). The raw `heap`, the thread count and that per-thread figure are printed as `reported` and written under `reported` by `--json`; they are not in `counts.json`. Every run sets `GOMAXPROCS=1`: a native core's heap grows with its runtime's processor count, which would otherwise follow the CPUs of the machine. A number above its baseline fails. A number below it also fails until `counts.json` is lowered in the same change, so a gain cannot be lost later without notice. A full check takes about a minute. The counts are not timings: wall-clock latency stays with the nightly durable-bench session.

Each run also prints the statements, the core's heap allocations (`mallocs`) and bytes allocated (`allocBytes`, which for a Go build is runtime/metrics' `/gc/heap/allocs:bytes`) per warm turn and its collections per 100 warm turns, and `--json` writes them under `perTurn`. They are the warm sums divided by the warm-turn count, so with the default 5 warm turns the collections move in steps of 20 per 100 turns. `check` refuses a baseline measured with another `--turns`, so the ratchet on the sums already gates them, and they are not in `counts.json`.

### Replay counts (`lib/instr.mjs`, `durable/core/contracttest/counts`)

The same cells also hold what the core costs on a recorded workload. `counts.mjs` runs the bench workload on each fixture on the native stack with `bench --record`, which writes everything the core was given: its events, the answers to its `pig.read` calls and the ids `pig.uuidv7` returned. The core is a pure function of that stream (ABI section 3, ADR D8), so `countgate` replays it through each build with no storage, no model and no clock, compares every step with the step the recorded run produced, and only then keeps a count:

| metric | wasm-tinygo, wasm-go | native |
|---|---|---|
| `cold.instructions`, `warm.instructions` | Wasm instructions executed, on the shipped module instrumented with one counter increment per basic block (`contracttest/fuel`), run under wazero | |
| `instructions` | | instructions of `core.(*Session).Step` under callgrind (the pinned valgrind named by `VALGRIND`) |
| `cold.allocCalls`, `cold.allocRequestedBytes`, `warm.*` | calls of TinyGo's `runtime.alloc` and the bytes they asked for, by function name; the Go wasip1 build keeps no names, so it has none | |
| `cold.gcCycles`, `warm.gcCycles` | collection cycles TinyGo's collector ran: calls of `runtime.runGC`, which every cycle runs (the allocator's own and an explicit one), counted by function name like `allocCalls`; the same count `runtime.MemStats.NumGC` reports, with no ABI field. The replay's `Live` pass (which collects at every interval end) is separate, so the count is the workload's own | |
| `warm.liveGrowthPerTurn` | bytes the live heap (`mem_stats` slot 6, after a full collection) grows per warm turn: the slope from the lowest reading of the first n/2 warm turns (rounded down) to the lowest of the last n/2, so one object a conservative collector keeps live at one reading is not growth | the same from the Go heap, as the median of 9 replay processes of one recording: the Go runtime makes one-off 16 to 64 B objects (such as a type-assertion cache, rebuilt on a random 1 in 1,024 misses) at a random turn of each process, so one process reads up to 37 B/turn where the next reads 0 |
| `warm.settledLiveGrowthPerTurn` (cells 50 and 3500) | the same slope over the last 30 of 60 warm turns of a 61-turn recording of the fixture (`countgate measure --settle 30`). The 5 warm turns of `warm.liveGrowthPerTurn` read 0 for a set that keeps bytes every turn in steps they straddle; leaving 30 warm turns out keeps a bounded early step out (TinyGo rises by 10,176 B in three steps by warm turn 19 and is then flat for 100 turns, which an unsettled 30-turn slope reads as 336 B/turn) | |

The settled window fails closed. A bounded step that happens after warm turn 30 lands in the gated turns and reads as growth. Do not widen `settle`, shorten the window or add slack to pass such a step. Find out whether the step is bounded with a longer run of the same recording, then record its turn and size or fix the retention. A window that leaves out more turns also hides retention that starts late.

The Wasm runs repeat to the byte, so those counts, the instruction counts included, are exact; the native counts move with Go's randomized map iteration (`DEFAULT_TOLERANCE` in `lib/counts.mjs`). `instructions` is the exact count of basic-block lengths and has a native counterpart. The two native instruction counts have different scopes: `instructions` covers the core's steps on the replay (it isolates a core regression and compares with the Wasm column), and `ir` covers the native process on the live cell outside the clocked packages (the code around the steps as well, where a host regression shows). The instrumenter counts one unit per instruction of the binary and adds no function, block or label. It refuses SIMD, atomics and tail calls, which the core does not use.

`countgate replay --rec FILE [--tinygo core.wasm] [--go core.wasm]` replays any recording, including one a Durable Object's flight recorder wrote (`host/cf/src/observe.ts`, `host/PROTOCOL.md` "Observability and the flight recorder"), natively and under both Wasm builds: a trap on the recorded events happens again on the developer's machine.

## Robustness proofs (`cli/robust.mjs`)

Proof-style checks of a build that a long soak would only sample. Each is a bounded number of real processes. Build the cores first (`make -C durable/core build-tinygo build-go`, `sh durable/core/budget/build-native.sh`); a candidate runs through its JS facade, so the Wasm cores and the native core (cmd/corenative) share one host.

```sh
node cli/robust.mjs induction --impl wasm-tinygo --n 500 --warmup 50   # retained memory at W+N, W+2N, W+4N turns of one Harness
node cli/robust.mjs crash --impls wasm-tinygo,wasm-go,native            # kill scenarios/crash-conversation.mjs after every commit, restart, compare
```

- **Memory induction.** One Harness on an empty store runs W + 4N turns of the bench workload (8 lookups a turn, the incremental scripted model). After every turn `CONTRACT_MEM_HEAP=1` makes bench.mjs run a full collection in each core and in the host and record `heap` (the core's live heap: `mem_stats` slot 6, which runs the collection), `reserved` (Wasm linear memory, or a native core's runtime `Sys`), `jsHeap` and `wallMs` (the real clock; detenv makes the scenario's clock virtual). Each point's value is the envelope, the largest sample in the interval ending there ((W, W+N], (W+N, W+2N], (W+2N, W+4N]), so a core that releases in batches (a sawtooth with a period under N) measures the same at any phase. The step is the growth per turn of the envelope from N to 2N and from 2N to 4N; a core that keeps nothing per turn shows ~0 bytes per turn in both, and the bound judges the second. A run that stops early (a trap at the memory cap) is incomplete, never bounded.
- **Soak.** `robust soak` runs `--objects` Harnesses in one process (bench.mjs `--objects`: the Durable Objects of one isolate) on empty stores for `--rounds` turns each, with compaction on and a `--window`-token model (100,000 by default: each conversation compacts about every 50 turns, so the model's context stays bounded while the stored history grows). `CONTRACT_MEM_EVERY` makes bench.mjs sample every `--every` rounds. The judge is the induction step with W = `--warmup` and N = (rounds - W) / 4, per object-turn. The nightly run is 4 objects x 5,000 rounds (20,000 turns).
- **Time travel.** `robust timetravel` runs scenarios/time-travel.mjs on a copy of the `--size`-turn W1 fixture: locate turn h`--at-turn` by scanning entries, an as-of context there, `snapshotAsOf` of the agent document, `--scrub` as-of contexts spread over the conversation, and a fork at h`--at-turn` with one turn on it. Pi runs first; each build must equal Pi at every level (commits, model contexts, printed lines, store). It prints the wall time of each operation beside Pi's.
- **Exhaustive crash points.** `scenarios/crash-conversation.mjs` is one conversation with tool turns, a foreground sub-agent, an inbox (a follow-up, a steer and a write that arrive while a turn runs), background compaction and a fork, written to be restarted: every submission has a request id, the fork is found before it is created, the sub-agent finds its child. For every commit of the uninterrupted reference run (about 160), Pi and each build are killed after that commit (SIGKILL, WAL kept) and restarted with the same program until it finishes. A crash point passes when the build's store at the crash and its final store equal Pi's under the same crash, its restart's commits, contexts and output equal Pi's restart, and its final ledger equals the uninterrupted run's: every client request id has one submission with the uninterrupted status, and every user input is in its conversation once. Each final store is also compared with the uninterrupted store; a restarted process mints new session ids and timestamps, so Pi's own final store differs there, and the build must differ exactly as Pi does.
- **Long histories.** `tools/extend-fixture.mjs <store> <from> <to>` appends W1 history turns with pi-durable and the incremental model, so a 10,000- or 25,000-turn store takes minutes instead of hours. It works on a contract fixture or on a durable-bench fixture's Durable Object database; extending 250 to 1,000 and 1,000 to 3,500 turns reproduces the published seed fingerprints.

### The memory bar (`counts.mjs bar`, `make durable-bar`)

`counts.json` records what a build costs; the Durable 0.5.0 bar says what it may cost. `bar` fails when `warm.liveGrowthPerTurn` is not 0 in any build's cell (exactly 0 for the Wasm builds; for native, within the 32 B/turn by which a native core that keeps nothing moves between runs, `ABSOLUTE_TOLERANCE`), or when a build's heap at the largest fixture exceeds 1.1x its heap at the smallest (`BAR_HEAP_SIZE_RATIO` in `lib/counts.mjs`): memory is bounded by the model's context window, never by conversation length. It judges the committed file without a run (`--from`), or a fresh measurement. It is part of gate stage 8. It fails on any build that keeps memory per turn, and no number in it may be raised to pass.
