# rev-fix-99-test-tempdirs: review of fix-99-test-tempdirs

Reviewed: `fix-99-test-tempdirs` at `c373fe1cd` (base `e95e56c47`).

Verdict: ACCEPT-WITH-FIXES. The design is sound: each heavy Go package scopes `TMPDIR`/`TMP`/`TEMP` to a root that `TestMain` removes, the inventory tests remove their fixtures on exit and on signals, and `make test` groups fail on leftovers. On the base commit, `go test` of the 16 affected packages under a private `TMPDIR` leaves 292 entries (`pig-packed-packed-node-*` 110+, `pig-command-tests-*` 16, `pig-ext-*`, `pig-packed-*-auth-cell`, `pig-clipboard-*`, `pig-tools-other-*`). On the reviewed branch, `go test -p 24 ./...` under a private `TMPDIR` leaves 0, with and without `XDG_RUNTIME_DIR`. Five findings were fixed on this branch. Three residual items are outside this change.

## Findings fixed

| # | finding | red evidence | fix |
|---|---|---|---|
| 1 | Longer temp paths broke five tests. The per-package root plus `make test`'s `pig-tests.XXXXXX` scratch adds about 39 bytes. The report's "failing set identical to baseline" used a 12-byte `TMPDIR` and did not cover this. | With a 24-byte `TMPDIR` (CI's grouped scratch is 21 bytes), these tests fail on the branch and pass on the base commit: `TestNodeProviderSocketDrainsPendingEnvelopesBeforeWorkerExit` and `TestNodeProviderSocketRejectsBadFrameBeforeNextEnvelope` (`bind: invalid argument`), `TestPortWave12CloudflareAIBinding` (`Unix socket path is 118 bytes`), and `TestInteractiveResumeIntoAnUntrustedProjectPromptsForTrust` plus `TestInteractiveResumeWithAMissingCWDShowsPisStatuses` (the Session name falls out of the 140-column picker). | The socket tests use `shortSockDir`. The Cloudflare test gets a short `XDG_RUNTIME_DIR` under the scoped root. `startInteractivePig` uses `shortTempDir` for its home. All five pass with the same `TMPDIR`. |
| 2 | `TestLinuxTestShardsPartitionPackages` failed. Its fixture root lacked the new `assert-clean-tmp.sh` (exit 127), and CI runs `go test ./test/ci-images`. No test checked that `test-grouped.sh` wires the leak check. | Failed on the branch, as observed. | The test copies the script. The new `TestGroupedTestsFailOnTemporaryLeakAndRemoveScratch` makes the fake `go test` leak into its `TMPDIR`. The group must fail with the leak reported and remove its scratch directory. Two mutations fail the test: dropping `TMPDIR="$SCRATCH"`, and dropping the `assert-clean-tmp.sh` call. |
| 3 | `make interface-inventory-test` had no leak guard. `scratch.test.mjs` tests the helper but not the tests that use it. | Reverting `cli-inventory.test.mjs` to `fs.mkdtempSync(os.tmpdir(), …)` leaked six `pig-cli-inventory-*` directories, and the target passed. | The target runs `npm test` under a private `TMPDIR`/`TMP`/`TEMP`, removes it on exit, and fails through `assert-clean-tmp.sh`. The same mutation now fails the target, and the unmutated tree passes. |
| 4 | `assert-clean-tmp.sh` allowed `go-build*` as a "shared Go cache". It is one go command's `GOTMPDIR` work directory, and go removes it on exit. A remaining `go-build*` directory comes from a killed child build. | `TestAssertCleanTmpFlagsLeaksAndAllowsSharedCaches` expects `go-build123456` to be reported. The test fails before the fix. | The allowlist is `node-compile-cache` and `v8-compile-cache-*`. A full suite run leaves no `go-build*` directory. |
| 5 | `make source-hygiene` failed. The progress report named the private build host and branch namespace, but it said source hygiene was clean. | `check-scratch-paths` reported three lines. | The names were removed. `make source-hygiene` passes. |

## Checks with no finding

- Collisions: every root comes from `os.MkdirTemp`. `RunScoped` and each `TestMain` remove only their own root. `test-grouped.sh` gives each group its own scratch directory. No path scans or deletes by prefix.
- Cleanup paths: every early `os.Exit` after the root is created removes the root. A removal failure turns a passing run into exit 2. No run printed "remove scoped/isolated" errors.
- Guards: removing `ScopeTempDir` fails `TestTempDirIsScopedToThePackageRun`, and `TestExtensionHostLogsStayInScopedTempDir` binds the host log path. The guards test scripts and helpers, not workflow YAML.
- Weakening: the moved Windows `TestMain` keeps both report hooks. No assertion was removed. The `extensioncorpus` change reports a cancelled parent instead of recording a false timeout, and `TestValidateExampleCancelledRemovesScratchHome` covers it.
- Windows and macOS: `GOOS=windows go vet` (including `-tags integration ./test/integration`) and `GOOS=darwin go vet` pass on every changed package. `os.TempDir` reads `TMP` on Windows, which `ScopeTempDir` sets.
- Rust SDK: `cargo test` under a private `TMPDIR` leaves nothing.

## Residual items (not fixed here)

1. Parity runs. A PiG run whose extension fails to load keeps its D56 stderr log in the inherited `TMPDIR`. For example, the scenario 32 command (`load-failure.ts`) leaves one `pig-packed-packed-node-*.log`. The printed error does not name that log. `make parity` does not run under a private `TMPDIR`. Scoping it changes the temp root that parity cwd and footer-crop checks measure, so the change needs a complete `make parity` pass. Separately, D56 says logs are removed "unless a load or lifecycle diagnostic names them", but the print-mode diagnostic does not show the path. That production behavior needs an owner decision.
2. Windows removal. `RunScoped` calls `os.RemoveAll` once. The `testing` package retries Windows sharing-violation errors for 2 s. A Windows file lock at the end of the package can turn a pass into exit 2. This was not observed, and it cannot be tested on Linux.
3. Python SDK tests use `tempfile.mkdtemp()` without removal (`tmp*` prefix). Nothing in `make test` runs them.

## Commands

- `TMPDIR=<private> go test -p 24 -count=1 ./...` on the branch (19- and 24-byte roots) and on the base commit (failing packages), with fixture exports from `automation/ci/test-fixtures.sh --exports`; failing-test sets compared with `comm`. The remaining failures are the same on base and branch: missing Xvfb, the shared `.upstream` mirror and the Pi pin.
- The same packages without `XDG_RUNTIME_DIR`.
- `TMPDIR=<private> make interface-inventory-test`, with and without the mutation.
- `go test ./test/ci-images`, with the two `test-grouped.sh` mutations.
- `make lint-changed`: 0 issues. `make source-hygiene`: clean. `GOOS=windows|darwin go vet` on changed packages: clean.
