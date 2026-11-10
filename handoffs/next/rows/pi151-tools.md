# Rows for the Pi 1.5.1 tool fixes (PortTools151, branch fix/pi151-tools)

## test-mapping-v1.5.1.json entries (disposition ported; hashes are the file's own)
- `packages/coding-agent/test/find-abort-before-spawn.test.ts` sha256:f2925681665f816a98a8f6d4079eaa08e5ea8718c830410e7ac5072e4f9781d8 -> evidence ['internal/codingagent/tools/find_pi151_test.go#TestFindDoesNotStartFdAfterAbortDuringGitLookup']
- `packages/coding-agent/test/grep-operations.test.ts` sha256:1de83e88c03775b8adecafcd7278bf9bdbd9ddc3f5674cc756a1cb042e3e1c63 -> evidence ['internal/codingagent/tools/grep_operations_readfile_test.go#TestGrepOperationsAcceptsADeprecatedReadFileHookWithoutCallingIt']
- `packages/coding-agent/test/powershell-tool.test.ts` sha256:656774d3402cff510da1bf78679819c063adaaacb2084602aa03cbb75b3240cd -> evidence ['internal/codingagent/tools/powershell_test.go#TestPowerShellArgsUseProcessLocalExecutionPolicyBypass', 'internal/codingagent/tools/powershell_exit_codes_test.go#TestPowerShellExitCodes', 'internal/codingagent/tools/powershell_exit_codes_test.go#TestPowerShellCommandAppendsTheExitSuffixForWindowsPowerShellOnly']
- `packages/coding-agent/test/suite/regressions/3302-find-path-glob.test.ts` sha256:683a4c5570c743ef336d1441c30d10451ca8890b2cb5c6240aee6d878e8577cc -> evidence ['test/parity/scenarios/tools/05-print-find-tool-exec.toml', 'docs/parity/pending-tests-triage.md', 'internal/codingagent/tools/find_regressions_test.go#TestFindPathGlobRegression', 'internal/codingagent/tools/find_pi151_test.go#TestFindPathPatternsPi151', 'internal/codingagent/tools/find_pi151_test.go#TestFindFdInvocationWindowsPathPattern']

## upstream-sync/v1.5.1.toml (disposition = ported)
- core/tools/find.ts: evidence test:internal/codingagent/tools/find_pi151_test.go#TestFindFdInvocationWindowsPathPattern, #TestFindDoesNotStartFdAfterAbortDuringGitLookup; Pi 463b8f89 + a07f2512 (find.go newFindFdInvocation, findPathFilter, internal/minimatch NoExt). Windows fd end to end only on Windows CI.
- core/tools/grep.ts: evidence test:internal/codingagent/tools/grep_operations_readfile_test.go#TestGrepOperationsAcceptsADeprecatedReadFileHookWithoutCallingIt; Pi 244e5f5d (GrepOperations.ReadFile in tools and the Go/Py/Rust/Lisp SDKs, conformance grepdir case).
- core/tools/powershell.ts: evidence test:internal/codingagent/tools/powershell_exit_codes_test.go#TestPowerShellCommandAppendsTheExitSuffixForWindowsPowerShellOnly, #TestPowerShellExitCodes (Windows CI only); Pi c9dd2c53 (powershell.go powerShellCommand).

## PORT_MAP
- find.ts, grep.ts, powershell.ts rows keep their Go files; no new Go file maps a Pi source.

3302 row rationale (carry from the 1.5.0 row): audited all patterns and fixture paths in the Go test; removing --full-path compiles and fails the path-glob cases; the print-session scenario 05-print-find-tool-exec.toml enables find and echoes its actual result. Pi 1.5.1 adds src/direct.spec.ts, `src/*.spec.ts`, braces/classes, the limit and the Windows backslash cases (find_regressions_test.go, find_pi151_test.go).
