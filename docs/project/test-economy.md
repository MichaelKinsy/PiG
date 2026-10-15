# Test economy

PiG's suites are large: `make test` takes about 7,800 CPU-seconds and 31 minutes on a 196-core host, and `make ci-drift ci-contracts` about 1,400 CPU-seconds. Many agents and developers run tests on one host at the same time. This document defines which tier of tests to run when, and the rules that keep each run's cost proportional to the change. CI and the release preview still run every suite in full. Nothing here removes a test or lowers what CI proves.

## Tiers

| Tier | When | Commands | Cost |
|---|---|---|---|
| Inner loop | After each edit | `go test ./<pkg> -run '<Tests>'` for the code you edit | seconds |
| Change gate | Before each commit you hand off | `make test-changed`, `make lint-changed`, `go vet` and `GOOS=windows go vet` on the packages you touched | minutes |
| Contract gate | Once before you report a change ready | `make ci-drift ci-contracts`, plus `make parity-family FAMILY=<f>` for each parity family the change reaches | minutes |
| Full suites | CI, the integrator, and the release preview | `make test`, `make check`, `make verify`, `make parity`, `make vet`, `make lint`, `go test ./...` | tens of minutes |

`make test-changed` tests the packages a change can affect. `automation/ci/changed-packages.py` maps each file changed since the merge base with `TEST_BASE` (default `@{upstream}`, then `main`) to Go packages:

- the package whose directory holds a changed Go file;
- the package that embeds a changed file;
- the package whose directory, or a `testdata` directory under it, holds a changed file;
- a package whose Go source names the changed file's repository path, as one string or as the quoted components of a `filepath.Join` call. A file at the repository root (`Makefile`, `README.md`) also names files of other directories, so its bare name counts only as a relative path (`"../../Makefile"`) or a later `filepath.Join` argument (`root, "Makefile"`).

It then adds every package whose tests import one of those packages, directly or transitively (the reverse closure of `go list -test -deps`). The edited packages run under `-race`; the other affected packages run without it (`TEST_CHANGED_RACE=affected` or `none` changes this). A change to `go.mod`, `go.sum` or `go.work` affects every package. The script names packages that build only under a tag (the parity runner, the integration tier) and files that no package test reads; their own make targets and CI cover them.

`make test-changed` prints each package result, the output of failed tests, and the slowest tests and packages. The raw `go test -json` events stay in `tmp/test-changed/<time>/`.

## Rules for running tests

- Do not pass `-count=1` unless the test reads an input through a child process. The go test cache keys a result on the test binary, the environment variables the test read and the files the test process opened. It does not see what a child process reads: a script, a Node or Python oracle, a fixture build. A test that runs a repository script reads the script itself first (`repoScript` in `test/ci-images`), so an edit to the script reruns it. A test or build whose child process reads other files keeps `-count=1`; the make recipe says why (`docs-drift`, `standard-check`, the parity runner). A failure is never cached.
- CI runs every test. It restores the Go build cache, test results included, from earlier commits, so `test-grouped.sh`, `test-race.sh` and the Go test recipes pass `-count=1` when `CI` is set.
- Use `-race` for the packages you edited and for concurrency work, not for the whole module.
- Do not run `go test ./...` or `make test` to check one change. Use `make test-changed`.
- Bound fuzzing. `go test -fuzz` starts one worker per `GOMAXPROCS` by default, which on a 196-core host is 196 workers. Pass `-parallel` (at most 4 on a shared host) and `-fuzztime` (at most `2m`). Long fuzzing campaigns run on a dedicated machine.
- Build with `-trimpath` on a shared host. Without it every package's build-cache key contains its absolute directory, so each checkout recompiles the whole module: 280 CPU-seconds for `go build ./...`, 776 for `go vet ./...` and 921 for `GOOS=windows go vet ./...` in a fresh worktree. With `-trimpath` a second checkout of the same source links only.
- Set `GOCACHE` and `GOMODCACHE` explicitly. The go command derives both from `HOME`, so a test or script that replaces `HOME` would otherwise compile into an empty cache.
- Set `PIG_NODE_MODULES_STORE` to a host directory to share the locked Node trees (`extensions/sdk-ts`, the interface extractor, `durable/interop`) between checkouts. Each checkout otherwise holds its own 333 MB copy.
- `make port-lint` and `make interface-gaps` replay a stored passing run while no tracked, modified or untracked file has changed (`automation/ci/cached-gate.sh`). A failure always reruns. `PIG_GATE_CACHE=0` forces a run; CI always runs them.

## Rules for writing tests

- Find files from the package directory, not from `runtime.Caller`. `go test` runs every test binary in its package directory; `testenv.PackageDir` and `testenv.ModuleRoot` return it and the module root. Under `-trimpath`, `runtime.Caller` reports a module-relative import path, not a file system path.
- Start an expensive oracle (a Node or Pi process, a built binary, a type-checked module) once per package with `sync.Once` or `TestMain`, not once per case.
- Wait on a channel, a file, a process exit or a condition, never on `time.Sleep`. A sleep makes the test slow on an idle host and flaky on a busy one.
- Stop every process a test starts, on every path. Kill the process group in `t.Cleanup` when the process may start children or ignore signals. `make test` fails a group that leaves a process running under its scratch `TMPDIR` (`automation/ci/reap-test-processes.sh`) and kills it.
- Scope temporary files. A package whose tests start pig, build binaries or create temporary files outside `t.TempDir()` calls `testenv.RunScoped` or `testenv.ScopeTempDir` from `TestMain`. `make test` fails a group that leaves anything in its scratch `TMPDIR`.
- A test that runs the go command with a fresh `HOME` writes `go/telemetry/mode` containing `off` into that home's configuration directory. Otherwise the go command starts a detached telemetry child that writes into the directory after the test removed it.
- Keep CPU-heavy loops (fuzzing, mutation, stress repetition) out of the default run. Gate them behind an explicit flag or environment variable and run them in a dedicated job.

## Test pruning

A test earns its CI minutes by catching a bug. Tests are classified by where their expected values come from:

| Class | Expected values come from | Action |
|---|---|---|
| A | Running Pi, Pi's own tests, or a cited Pi source line | Keep. |
| B | A rule over generated or looped inputs (invariant, property, fuzz) | Keep. |
| C | PiG's own output: a golden regenerated from PiG, a value copied from a PiG run, an expectation edited to pass when the code changed | Convert to an A test if the behavior is Pi's, to a B test if it is PiG's, and delete it if neither adds coverage. |
| D | Nothing: it checks that a type, method or field exists | Delete. The interface ledger proves existence. |
| E | A rule another test already checks: it adds no statement or branch coverage over the A and B tests and the journeys | Delete. |

`go run ./test/parity/cmd/testprune` finds and verifies the candidates:

- `inventory` classifies every Go test function from mechanical signals (it runs Pi, reads a golden, only reflects, cites Pi). The classes are a starting point for review.
- `history` lists test files whose expected values changed in a commit that also changed production code of the same directory.
- `redundant -pkg P` runs each test of `P` alone under coverage and lists the tests whose blocks other tests repeat. It never lists a test with no in-package coverage, an A or B test, or the last cover of a block.
- `redundant -journeys FILE` counts a block that the end-to-end journeys reach as covered by another test (`JOURNEY_PIG_VARIANT=cover JOURNEY_COVERDIR=DIR go test -tags parity ./test/journeys`, then `go tool covdata textfmt -i=DIR -o FILE`). `-only-classes D-shape` limits the candidates to one class. `journeycover -pkg P -tests A,B -journeys FILE` lists the blocks of the named tests that the journeys do not reach, with a verdict per test: `journeys_cover_all_blocks`, `keeps_blocks`, `no_in_package_coverage` (a reflection or declaration guard, which the journeys never stand in for), or `protected` (a class in `-classes` or an oracle, ordering or cancellation name). Build the journey profile from the same commit as the package: a block is matched by file, position and statement count. Executing a block is not asserting on it: a journey asserts the rendered screen, not each edge case of a helper, so a list from these flags is a proposal that a mutation run must confirm, and it never covers tests that port a Pi test.
- `coverdiff -pkg P -against CHECKOUT` (or `-skip REGEXP`) runs the suite before and after a change and fails when a block loses all coverage. `-also PKGS` counts the tests of those packages as covering `P`. A deletion batch lists its lost blocks, and the list must be empty or covered by A tests, B tests or journeys.
- `mutate -pkg P -blocks FILE -out TSV` runs a seeded sample of mutants (operator swaps, negated conditions, off-by-one constants, flipped boolean returns) against the suite. Run it in the checkout before the batch and in the checkout after it, with the same seed, then `mutcompare BEFORE AFTER`: a mutant killed before and alive after fails the batch.

A pruning batch covers one package area and reports the test count, the lost-coverage diff, the mutation result and the lane-gate time before and after.

### Ratchet

- `make test-provenance` fails on a golden or snapshot data file that `test/testdata-provenance.toml` does not declare. A Pi golden names the script that regenerates it from Pi. A PiG-owned golden names the invariant test that asserts the rule and says why a value snapshot is needed.
- `make test-expectation-lint` (and the `DCO` job on pull requests) fails when a commit changes an expected value in a test file together with production code of the same directory, unless the commit cites the Pi source line whose behavior changed: a `Pi-Behavior-Change: <pi file.ts:line>` trailer, or a `name.ts:line` citation on a line the commit adds to the test file. The cited file must be a Pi source or test file that `docs/parity/PORT_MAP.md`, `test/parity/interfaces/test-mapping-v*.json` or `test/parity/upstream-sync/*.toml` names. Adding a test, renaming an identifier, moving a file or package path and refactoring code without changing an expected value pass. Commits authored before `expectations_from` in `test/testdata-provenance.toml` are not checked. A test that fails after a code change is a finding about the code until a Pi source line says the behavior changed.

## Top costs measured

Measured on a shared 196-core, 1 TB development host at commit `54220fb62` on 2026-10-07, each command alone in a systemd user scope. CPU is the scope's total CPU time.

| Rank | Cost | CPU-seconds | Wall | Peak memory |
|---:|---|---:|---:|---:|
| 1 | `make test`, all groups (fast 2,773; `cmd/pig` 8 shards 2,908; conformance 1,136; subprocess 1,005) | 7,822 | 31 min | 6.6 GB |
| 2 | A fresh worktree's first build per configuration, without `-trimpath`: `go vet ./...` | 776-940 | 32-36 s | 6.6 GB |
| 3 | `GOOS=windows go vet ./...` in a fresh worktree | 921 | 35 s | 6.9 GB |
| 4 | `make ci-contracts` (of which `interface-gaps` 431-512) | 753-794 | 4 min | 6.6 GB |
| 5 | `make ci-drift` (of which `port-lint` 570) | 612 | 52 s | 7.0 GB |
| 6 | `go test -race -count=1 ./internal/codingagent ./tui` | 439-651 | 4.5-6 min | 2.7 GB |
| 7 | `make test` cache warm-up: four `go build ./...` passes that relink every command | 270 per run when warm | 20 s | 2.8 GB |
| 8 | `go build ./...` in a fresh worktree | 280 | 25 s | 2.8 GB |
| 9 | golangci-lint on two packages in a fresh worktree | 264 | 26 s | 2.4 GB |
| 10 | Leaked processes: test binaries, Node extension cells and tmux servers from failed runs, still running hours to days later | 10 to 15 processes at a time | - | - |

In three days of coding-agent sessions on that host, 78% of 5,653 `go test` invocations passed `-count=1`, 26% passed `-race`, and 365 ran `go test ./...`.
