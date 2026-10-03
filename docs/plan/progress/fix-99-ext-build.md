# fix-99-ext-build: extension builds are fast, clean, and never block a resume

Base: `staging/porter/pi-0.99.1`. Isolation: every run used a temporary `HOME`, `PIG_HOME`, and `PIG_CODING_AGENT_DIR`; the owner's `~/.pig` and `auth.json` were not touched.

## Cause (confirmed by reproduction)

`GOROOT=<go1.26.7 install> <go1.26.1 install>/bin/go build` prints `compile: version "go1.26.7" does not match go tool version "go1.26.1"` for every standard-library package. The go command takes its tools from an inherited `GOROOT`. PiG passed `os.Environ()` to every build unchanged. mise exports `GOROOT` for the project directory, and its `go` shim picks a version from the directory it runs in. PiG builds in a generated temp directory, so the shim could pick another release than the shell's `GOROOT`. The build failure was not a "compiler diagnostic", so it was never recorded and every start rebuilt and failed again. Each cell printed its own notice and the full output, plus the generated go.mod.

Reproduced end to end with the built `pig` (13 Go factory extensions, `PATH` = go1.26.1, `GOROOT` = go1.26.7): before, 794 lines / 34,264 bytes on every start (2.4 to 2.6 s each); after, the build succeeds.

## Changes

1. Toolchain: `internal/toolchain.ResolveGo` resolves the go command once per process (memoised, concurrency-safe). A command inside its own installation is used as found without running it. A shim or distribution binary is asked `go env GOROOT` with the inherited `GOROOT` removed, and the installation's own binary is pinned. `GoToolchain.Environ` replaces the inherited `GOROOT`. Wired into packed cells, source builds, Piglet Binary builds, `pig build`, and the Piglet toolchain record. A remaining mismatch (broken installation) is one line naming both releases and the fix.
2. Output: one rewritten status line for a load pass (`Building extensions [n/N] ...`, cleared at the end, silent on a warm start). `BuildFailure` is one summary line plus a log path; the complete output and go.mod go to `<config-root>/cache/logs/` (newest 64, 4 MiB each). Failures with one cause are grouped (`13 extensions failed to build: <cause> (names; details: <log>)`) in startup diagnostics and `/reload` issues. Go and Rust builds.
3. Failure caching: compiler diagnostics and toolchain mismatches are recorded against the cell's inputs. The cell hash now includes the toolchain root and the compiler binary's identity, so repairing the installation changes an input. A mismatched toolchain is met once per process. A recorded failure prints the retry command (`pig extensions cache prune --failures`). Network, module and disk failures are never recorded. Rust packed cells use the same cache. Source-mode (non-factory) builds get the summary and log but no recorded failure: their content hash has no toolchain input, so a recorded failure could outlive the fix.
4. Resume latency: see the table. Extension loading stays before the TUI, as in Pi's `createRuntime`; a cold compile now shows progress at about 0.3 s instead of a blank screen. `--session` of a 4,600-message file is about 0.3 s of the startup (`session-created` minus `pre-runtime`).
5. Cache growth: `pig extensions cache` already existed (stats/prune, 30-day retention). Added `--failures`, and a 5 GiB size limit to the daily automatic prune (oldest use first; current and in-use entries are never evicted for it). Added `pig piglet prune [--keep n] [--max-size s] [--dry-run]` for built Piglet Binaries (approved by the lead; pulled installs never touched; no automatic artifact pruning). `internal/bytesize` holds the shared size parser.

Additive-features entries: D20 (toolchain, output, failure cache, cache limit) and D18 (`pig piglet prune`).

## Numbers (4,600-message session, `test-faux`, tmux 120x40, warm Go build cache)

| Case | Before | After |
|---|---|---|
| No extensions, first paint | 575-824 ms | 569 ms |
| 13 Go extensions, warm cell cache | 544-553 ms | 544-553 ms |
| 13 Go extensions, cold (first compile) | blank screen until 6.5 s | status line at ~0.3 s, ready 6.6 s |
| Owner scenario (`GOROOT` of another release) | 2.4-2.6 s and 794 lines on every start, no extension loaded | build succeeds; 237 ms next start |
| Broken installation (compiler of another release), 13 extensions | 2.4 s and 794 lines every start | first start 469 ms and one line; second start 163 ms and one line, no compile |

## Evidence

Red tests (each mutation-checked): `TestResolveGo*` (`internal/toolchain/resolve_test.go`), `TestBuildGoPackedCellIgnoresInheritedGOROOT` (fails with the `package time is not in std` error when `Environ` is a no-op), `TestBuildGoPackedCellReportsReleaseMismatchOnceAndRecordsIt` (fails when the in-process memory or the compiler identity in the hash is removed), `TestBuildGoPackedCellCompileErrorIsOneLineWithLog`, `TestRustBuildFailureSummarizesRustcDiagnostic`, `TestBuildLine*`, `TestColdBuildOfManyCellsIsOneStatusLine` (pty), `TestGroupLoadErrors*`, `TestBuildFailureIssuesForReload`, `TestExtensionLoadDiagnostics*`, `TestPruneRemovesRecordedFailuresOnRequest`, `TestAutomaticExtensionCacheGCEnforcesTheSizeLimitOldestFirst` (fails without the limit), `TestPigletPrune*` and `TestSelectPrunable*` (artifact removal mutation).

Changed existing tests: `TestBuildGoPackedCellRetriesTransientGoFailure` (its fake `go` is now its own installation and delegates with `GOROOT` removed, because PiG runs a `go` command with its installation's `GOROOT`); `TestPigletHelpListsCurrentSurface` (the surface gained `prune`); `TestParseCacheSize` moved to `bytesize.Parse` with the same inputs.

Commands: `go test -race -count=24` (GOMAXPROCS=4) on `internal/toolchain`, `internal/bytesize`, and the concurrent subprocess tests; `-count=6` on the runtimecell build tests; `GOOS=windows`/`darwin go vet` on the touched packages; `make lint-changed LINT_BASE=staging/porter/pi-0.99.1` (0 issues); `make divergence-guard`; `make source-hygiene`; `make interface-go` (regenerated `pig-go.json`) then the go and recommendations drift gates are clean.

Full package runs (`coding/extension/...`, `coding/piglet`, `coding/pigletbuild`, `cmd/pig`, `internal/...`): the only failures are tests that need the pinned Pi package (`extensions/sdk-ts/node_modules` is absent here), Xvfb, or already fail on the base commit (`TestPostLoginOAuthUsesSessionPersistenceAndThinking`, `TestRPCStdoutBackpressureLetsProviderFinishBufferedBody`, verified on a base worktree).

## Not done / notes

- `make generate` cannot run here (`parity-deps` refuses the shared `sdk-ts/node_modules`), so only `interface-go` was regenerated. `interface-inventory-drift`, `known-gaps` and `coverage` need that environment or fail on the base.
- Source-mode Go and Rust builds keep no recorded failure (see 3).
- A `go` wrapper placed on PATH outside a Go installation is bypassed: PiG resolves and runs the installation's own binary.
