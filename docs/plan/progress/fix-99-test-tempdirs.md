# fix-99-test-tempdirs: PiG tests must not leak temporary files

Base: `staging/porter/pi-0.99.1`. Branch: `fix-99-test-tempdirs`.

## Cause

The shared build host's `/tmp` held about 218,000 leaked entries (541 GB). Most of the listed prefixes are files, not folders, and they come from two creators:

| prefix | creator | why it leaked |
|---|---|---|
| `pig-ext-<name>-*.log`, `pig-packed-<cell>-*.log` (`pig-ext-sdk-fixture`, `pig-ext-fixture`, `pig-ext-ctx-mode`, `pig-ext-login-preview`, `pig-packed-packed-node`, `pig-packed-*-auth-cell`, and the rest) | production `coding/extension/host/subprocess/host.go` and `packed_go.go` (`os.CreateTemp("", "pig-ext-…")`) | The host removes the stderr diagnostic only on a clean shutdown and keeps the log of a crashed or failed extension on purpose. Tests that never call `Shutdown`, or that crash an extension deliberately, left the file in the shared `/tmp`. Hundreds of tests do this, so no per-test fix is complete. |
| `pig-cli-inventory-*`, `pig-interface-inventory-*`, `pig-test-inventory-*`, `pig-behavior-inputs-*`, `pig-correspondence-*` (plus `pig-wsl-keybindings-*` and six more prefixes in the same files) | `test/parity/interface-extractor/test/*.test.mjs`, `fs.mkdtempSync(os.tmpdir(), …)` | Never removed, except three subtests that registered `t.after`. |

The `TestMain` roots (`pig-command-tests-*`, `pig-subprocess-tests-*`, `pig-coding-tests-*`) leaked only when a run was killed or timed out.

## Fix

1. **Scope the temporary directory per test binary** (`internal/testenv/tempscope.go`). `ScopeTempDir(root)` sets `TMPDIR`, `TMP` and `TEMP`; `RunScoped(m, prefix)` creates the root, scopes it, runs the tests, removes the root, and turns a removal failure into exit code 2. Every `os.TempDir()` user, including host stderr logs, the bash tool's overflow logs, and child Node `tmpdir()`, now lands under a directory the test binary removes, whichever test made it and whatever cleanup it forgot. Wired into the `TestMain` of `cmd/pig`, `coding`, `coding/extension/host/{subprocess,cellpack,runtimecell}`, `coding/pigletbuild`, `coding/rpcclient`, `internal/codingagent`, `internal/codingagent/tools`, `agent/harness/{env,tools}`, `test/extension-conformance` and `test/integration`. Roots use short prefixes (`pig-sp-`, `pig-cmd-`, `pig-coding-`, `pig-cell-`, `pig-plb-`, `pig-ca-`, `pig-tools-`, `pig-env-`, `pig-htools-`), so a leftover from a SIGKILL is self-identifying and keeps socket paths short. Nothing scans `/tmp` at run time.
2. **Inventory tests** (`test/parity/interface-extractor/test/scratch.mjs`): `scratchDir(prefix)` replaces every `fs.mkdtempSync(os.tmpdir(), …)` and removes the directories on process `exit` (normal, uncaught error, `process.exit`) and on SIGINT/SIGTERM.
3. **Generators**: `test/parity/cmd/correspondence` and `test/parity/cmd/extensioncorpus` already removed their scratch directories with `defer`; a signal killed them before the deferred call ran. Both now run under `signal.NotifyContext`, so a signal cancels the context, the operation returns, and the deferred `RemoveAll` runs. `extensioncorpus` also stops treating a signal as a per-example "timeout" result.
4. **Suite-level guard**: `automation/ci/test-grouped.sh` runs each group under a private `TMPDIR` (removed on every exit path, including INT/TERM) and fails the group through `automation/ci/assert-clean-tmp.sh` if anything but the shared Go/Node caches remains.

## Guard tests

| family | test |
|---|---|
| each scoped package (13) | `TestTempDirIsScopedToThePackageRun` (`tempscope_guard_test.go`) asserts `os.TempDir()` is the package's scoped root and `os.CreateTemp("", …)` lands in it |
| subprocess | `TestExtensionHostLogsStayInScopedTempDir` loads a Node extension, does not shut it down, and asserts the stderr log is under the scoped root |
| `internal/testenv` | `TestScopeTempDirRedirectsParentAndChildren`, `TestRequireScopedTempDirAcceptsScopeAndRejectsShared` |
| inventory tests | `scratch.test.mjs`: normal exit, uncaught error, `process.exit`, SIGTERM each leave no `pig-*` directory |
| CI script | `test/ci-images/assert_clean_tmp_test.go` |
| generators | `TestWorktreeSnapshotDirectoryRemovedWhenCancelled`, `TestValidateExampleCancelledRemovesScratchHome` (not red-proven for the correspondence one: the deferred removal already existed) |

## Evidence

- Baseline (`TMPDIR=/tmp/f99base go test ./cmd/pig ./coding/extension/host/subprocess`, unfixed tree): 280 leftover entries, dominated by `pig-packed-packed-node-*` (about 170), `pig-ext-*` and `pig-packed-*-auth-cell` logs, and `pig-command-tests-*`/`pig-subprocess-tests-*` roots.
- Fixed tree, same packages, fresh `TMPDIR`: 0 leftovers, and the failing-test set is identical to the baseline (73 failures that need `npm ci` in `extensions/sdk-ts` and the pinned Pi package, which this worktree lacks; none is related to this change).
- Fixed tree, `TMPDIR=<scratch> go test -p 16 ./...` before the last three packages were scoped: 9 leftovers (`agent/harness/env`, `agent/harness/tools`: `tmp-*`; `internal/codingagent/tools`: `pi-bash-*.log`). After scoping them: 0 leftovers across the whole suite.
- Mutation: removing `ScopeTempDir` from the subprocess `TestMain` fails `TestTempDirIsScopedToThePackageRun`; disabling the `exit` handler in `scratch.mjs` fails all four `scratch.test.mjs` cases.
- `make lint-changed LINT_BASE=staging/porter/pi-0.99.1`: 0 issues. `divergence-guard`, `source-hygiene`, `docs-drift`, `go fix -diff`: clean. `GOOS=windows go vet` on the changed packages: clean.
- `make port-map-drift` fails on `packages/ai/src/...` rows. This change touches no file under `docs/parity`, `test/parity/upstream-sync` or `test/parity/interfaces` (`git diff --stat staging/porter/pi-0.99.1...HEAD` on those paths is empty), and the gate reads the shared `.upstream` mirror symlink, so the failure comes from the environment, not this branch.
- The base branch has moved 120 commits since this branch was cut; none of this change's files were modified upstream of it in a way this lane checked. Merge on integration.

## Not done

- The existing 218,000 leaked entries on the shared build host are not deleted by this change. Remove them with `find /tmp -maxdepth 1 -name 'pig-*' -mtime +2 -delete` once the new tests are in use.
- A test process killed by SIGKILL or a Go test timeout still leaves its scoped root. That root is a single `pig-<family>-*` directory, not thousands of files.
