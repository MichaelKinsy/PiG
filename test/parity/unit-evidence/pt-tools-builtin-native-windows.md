# Native Windows closure for builtin tool tests

The lead supplies native evidence at commit `ace100c3eadd875c5cd07269995350100e2c7fb3`, `native-proof/pending/`, on `staging/team/win/public-021-native`. The reported host is Windows 10 22H2, with the PowerShell run completed at 18:50Z. The retained logs independently identify Go 1.27.1 `windows/amd64`, Node 24.19.0, source commit `4f5e1c075c0f8799e2ac4efd30dab749b46be4e6`, and an unmodified checkout.

| Upstream test file | Native guards | Retained log |
|---|---|---|
| `packages/coding-agent/test/bash-close-hang-windows.test.ts` | `TestChildProcessCloseWithInheritedStdioPort`, both executor and tool cases | [W1](native-windows-tools/W1.log) |
| `packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts` | `TestFindRootRelativizationWindowsPort`, all five Windows rows; `TestFindCustomGlobRootPort` | [W2](native-windows-tools/W2.log) |
| `packages/coding-agent/test/suite/regressions/6596-taskkill-enoent.test.ts` | `TestTaskkillSpawnFailurePort`; `TestWindowsTaskkillSpawnOptions` | [W3](native-windows-tools/W3.log) |

Each command exits zero. All five named tests pass without a platform skip. The separate lead-command log at the same evidence commit runs the combined five-test filter through `go test ./...`; unrelated packages report no tests, not additional behavioral coverage.

The five Go test files have identical tracked bytes at the native source commit and integration commit `546f94b29`. Existing Linux/Pi comparisons and compiling mutations remain in the mapping. This evidence closes their previously explicit native-Windows validation obligation. It does not claim a native run of the entire current integration tree or qualify every Windows release path. No native Windows command is run on the Linux integrator host.
