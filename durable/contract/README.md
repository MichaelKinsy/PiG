# Durable core conformance gate

The gate every lane of the Durable core uses ([CONTRACT.md](../../docs/plan/durable-core/CONTRACT.md) sections 5-7). It runs the corpus (E00-E37, U, SC, I, W1-W4, X1, H) against pi-durable at the pinned reference commit (`PIN` `main`, Pi main `c23b9ec1` (1.5.0 + main), the commit PiG pins) and against a candidate, and compares the two at the store, commit, context, output and order levels (order: the scenario's API calls and the commits in one sequence, CONTRACT 5.8). One Pi commit is one row of `commit` lines, so a difference names the first divergent commit seq, the table, the column and whether the text differs in value, key order or number format.

## References

`PIN` names the reference: `main` is Pi main `c23b9ec1` (1.5.0 + main) (the commit PiG pins), the reference of every E, U, SC, I, W, X, H and F row (the tick rows F-ticks-*, F-split-* and `tools/gen-hopticks.mjs` included); the candidate runs over the same checkout. `release` names v1.0.4, whose stores the R rows read.

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

Every scenario runs in its own process with `HOME` and `TMPDIR` set to one fixed directory per scenario (`<tmp>/pig-contract/<id>`, serialised by a lock), so a path a scenario stores is the same for every implementation. A run always starts from an empty store directory. The scenarios' working directory is the reference's `packages/durable`, which E17 and E18 list with `ls`, so no run writes the reference checkout: a U row's vitest keeps its cache (`CONTRACT_VITEST_CACHE`, `cacheDir`) in the run's output directory, where its default, `packages/durable/node_modules/.vite`, made the first U row add `node_modules` to that listing and `contract all` (which captures the reference once, in its first pass) failed E17 and E18 on every fresh clone.

### Trace format

JSON Lines (CONTRACT 7.6): `meta`, `store` (path of each store opened), `sql` (statement text by index), `commit` (`store`, `seq`, `stmts` with `q`/`k`/`p`, `reads`, `changes` with `table`/`op`/`pk`/`before`/`after`), `model` (`call`, `model`, `ctx` sha256 of the pi-ai Context and options, optional `bench`, `messages`), `effect`, `uuid`, `mismatch`, `final` (per-table digests of each store), `out` (stdout lines), `exit`, `end`. Bench values: `bench` is durable-bench's 8-byte fingerprint of `[role, text, calls]`.

## Rows

`corpus.toml` is generated by `tools/gen-corpus.mjs` from the checkout (sha256 of every source at the pin). Regenerate after a pin change and read the `corpus --check` diff.

| rows | runner | what is compared |
|---|---|---|
| E00-E37 | `example` | the upstream example runs under the hooks; store, commit, context, output, order. E16 needs a provider key and is skipped |
| U-* (99) | `vitest` | the upstream test files, run from source; a row passes when every test the reference passes passes, the reference running the same file at the same time (a test both fail under the load of the moment is named, not counted) |
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

`ts` (`impls/ts-driver.mjs`, the vendored durable-core-ts control, `impls/ts-core/`) is the reference candidate: it passes W1, W2, W4c, X1, the H-50 matrix (68 crash points, 3 orders, plus the clean restart) and X1-live against Pi main. `test/matrix.test.mjs` breaks it four ways (key order, a missing `endedAt`, a usage total, recovery that does not requeue a task) and requires the matrix to fail naming the cause.

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

## Exclusive rows (`exclusive`)

A U row carries `exclusive = true` for one of two reasons, and the reasons are not the same kind. (1) Its upstream test asserts inside a real-time window that the run itself has to fill (`U-codemode`, `U-harness-tools`). `U-codemode`'s `times a script out across a restart` gives a script 300 ms to start the QuickJS sandbox and reach its first tool call, which costs the reference 150-230 ms of CPU; a process the host gives under about 65% of a CPU in that stretch misses the window, and the test then hangs until vitest's 5 s limit. The window binds the reference as well as the candidate. (2) Its upstream test has a total CPU budget that the reference clears with headroom and the candidate, which costs more CPU for the same test, does not clear once the gate's own load takes CPU away (`U-harness-ownership`, `U-harness-scheduler-retention`, `U-harness-idle-scopes`). Pi's 60 s for the 20,000-task chain costs the reference 24 s and the candidate 24.5 s (1.02x); vitest's default 5 s for 201 subagent cycles costs the reference 0.5-0.7 s and the candidate 1.4-2.0 s at 8 CPUs, 4.6 s at 1. No other row's reference misses such a budget; it is the candidate that does, so the pairing of the two runs cannot excuse it.

The gate's own load did both: its rows run `-j 8`, each U row with two vitest runs of four forks, so a full run starved either side (of six full runs at `-j 8` the candidate missed the `U-codemode` window in four and the reference in one; 8 concurrent runs of the retention row on 4 CPUs all ended at 10-11 s). The gate therefore runs an exclusive row alone, before any other row starts (`runRows`), and its reference run and candidate run one after the other, the reference first (`runVitestRow`). Nothing is retried and nothing is excused: a window or budget the reference misses is still a failure of that run and is named, and a window or budget only the candidate misses fails the row. `test/exclusive.test.mjs` fixes the order with stand-in rows, no clock involved. Mark a row only after probing that the test's window binds the reference too, or that the candidate alone cannot clear the budget under the gate's load, and keep that work out of the other rows.

### Cost bound (`cost_test`, `cost_max_ratio`)

Running a row alone removes the load that made a slow candidate miss Pi's budget, so a candidate that became several times slower would pass until it crossed the budget by itself. A row whose candidate costs more than about twice the reference's time for a test therefore records a bound: `cost_test` names the test (a prefix of its full name) and `cost_max_ratio` the largest candidate/reference ratio of that test's duration, both from the two runs of the same gate run, which take turns on an idle host. `costOf` (`lib/gate.mjs`) fails the row when the ratio is above the bound, with both times in the result, and a passing row says its ratio. The corpus check requires an exclusive vitest row, a test prefix and a number above 1. `U-harness-scheduler-retention` records `forgets ended subagent chains` at 5x. The measured ratio is 2.1-3.9 (median 2.5) in 13 paired runs at 8 CPUs on a host at load 30-140, one candidate run per reference run, alternating or back to back; the bound is 1.3 times the largest. The ratio, not an absolute time, because the host's speed moves both times together. The bound sees a candidate that costs about twice what it does now; a smaller slowdown is the budget's to catch. `U-harness-ownership` (1.02x) and `U-harness-idle-scopes` (1.5-2.1x in 5 alternated runs at 8 CPUs; 0.9 s reference, 1.6 s candidate against the 5 s budget) are under about 2x and record none. `U-harness-idle-scopes` is exclusive because it failed its 5 s budget on the candidate alone in 2 of 2 rounds of the 54 `U-harness-*` rows at `-j 8` in a 4-CPU container (the reference passing).

Where the retention cost goes (CPU profiles of the candidate at 8 CPUs: `node --cpu-prof` in the vitest worker with `--pool=threads`, and `runtime/pprof` in a scratch build of `cmd/corenative`; the facade made 41,376 calls to the core for the test, 202 of them uuid requests and 2 reads, 523 KB sent to the core): the candidate is about 1.0-1.4 s over the reference's 0.5-0.7 s, 25-35 us per core step. Part of it is the core and facade work every build does for 201 cycles of 206 steps (the in-process TinyGo build measured 1.3-2.6x the reference, median 1.75x, in five runs alternated with the native core's 2.1-3.7x, median 2.4x, on a loaded host), part is what a synchronous round trip over two FIFOs between two processes costs. In the facade's worker the blocking read of the answer is 0.68 s and the write of the request 0.16 s of the test's 1.9 s; the facade's own `tx` and `nodeStore` (the SQLite host) take 0.29 s; in the core process 35% of the CPU samples are read and write system calls and 28% the Go scheduler looking for the next runnable goroutine. Opening the FIFOs blocking (no netpoller) and `GOMAXPROCS=1` each left the test at 1.4-2.0 s, so neither is a defect to fix; no avoidable per-step round trip or allocation storm shows in either profile (202 uuid requests, 2 reads, no read or uuid per step). The cost is the rig's per-step process hop plus the core's per-step work, not a measured cost of the shipped Go Durable path.
