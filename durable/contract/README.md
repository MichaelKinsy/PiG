# Durable core conformance gate

The gate every lane of the Durable core uses ([CONTRACT.md](../../docs/plan/durable-core/CONTRACT.md) sections 5-7). It runs the corpus (E00-E31, U, SC, I, W1-W4, X1, H) against pi-durable at the pinned reference commit (`PIN`, Pi main `da866ada`, which writes task `startedAt`/`endedAt` and message `durationMs`) and against a candidate, and compares the two at the store, commit, context and output levels. One Pi commit is one row of `commit` lines, so a difference names the first divergent commit seq, the table, the column and whether the text differs in value, key order or number format.

## Run it

```sh
make durable-contract-setup                 # clone Pi at the pin into durable/contract/.cache, npm ci (needs git, npm, Node >= 24.19)
make durable-contract-test                  # the tooling's own tests, negative controls included (about 1 minute)
make durable-contract                       # the gate: pi (control), wasm-tinygo, wasm-go, native; a per-lane table
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

A row is `pass`, `fail`, `pending` (the implementation cannot run it yet, with the reason) or `skipped` (nobody can). `pending` never counts as `pass`; `--strict` makes it fail the run. A failed row is reported against the lane that owns it (`owner` in `corpus.toml`): `dcore-session`, `dcore-loop`, `dcore-store`, `dcore-host`, `do-core-design`. The lanes are the packages of docs/plan/durable-core/LAYOUT.md (`dcore-store` owns the records, the store and the compaction and fork rows).

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
| E00-E31 | `example` | the upstream example runs under the hooks; store, commit, context, output. E16 needs a provider key and is skipped |
| U-* (49) | `vitest` | the upstream test files, run from source; a row passes when every test the reference passes passes |
| SC | `vitest` | `registerStorageConformance` over the candidate's write encoder (pending until the encoder exists) |
| I | `interop` | `durable/interop/scenario.mjs` under the hooks |
| W1-*, W2-* | `bench` | `scenarios/bench.mjs`: ten 8-tool turns on a copy of the fixture, full-walk and incremental scripted model |
| W3-*, W4b-d, W4g, X1 | `bench` | streaming, restart per turn, bounded 32 KiB tool results, parallel round, retryable error, unknown fields |
| W4a, W4e, W4f | `bench`, `cassette` | pending or skipped, with the reason in `corpus.toml` |
| H-*, X1-live | `matrix` | the handoff matrix (sampled crash points for 3,500 turns) |

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
```

`--strict` makes a `pending` row fail, so a lane cannot claim a row the candidate cannot run yet. The command builds the candidate first when its paths in `impls.toml` are missing, passes `DCORE_*` and `DURABLE_CORE_*` variables (gate.sh's `DCORE_WASM_GO` and `DCORE_WASM_TINYGO`) through to the run, and exits 1 on any `fail`. Corpus ids are the ids of `corpus.toml` (`W1-50`, `H-50`, `E00`...); `corpus.tsv`'s coarse ids (`W2`, `W4a`, `H-W1`) are the ids with their size suffix.

A JS facade (`candidate_dir`) needs no bench module: the bench scenario program runs through it like an example. It provides `index.ts` and the modules the examples and the bench import by path (`env/node.ts`, `tools/index.ts`, `storage/sqlite/node.ts`, `storage/jsonl/node.ts`); a module the candidate lacks resolves to the reference's. Its storage opens stores through `globalThis[Symbol.for("pig.contract")]` (`openStore(path)`, `openDatabase(path)`) so every commit reaches the capture.

A facade candidate also names `wasm` (the core module its build produces, passed to the run as `DCORE_WASM`) and `setup` (a module of the facade, imported before the scenario program, that loads the core: `useCore(await WebAssembly.compile(readFileSync(process.env.DCORE_WASM)))`). A row stays `pending` while either file is missing; a facade that cannot find a core is not a failing core.
