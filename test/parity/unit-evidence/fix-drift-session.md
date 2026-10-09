# Session wire drift fixes

## Reference and scope

Base: `96284e76befed314dd69d4ef01b013ac6c40902e`. The oracle is real Pi 0.87.1 at `extensions/sdk-ts/node_modules/.bin/pi`. Source citations below resolve under `.upstream/current/packages/coding-agent/src/`.

| Finding | Root cause and fix | Pi reference | Guard |
|---|---|---|---|
| W15 | Clone copied raw branch rows, including obsolete labels. It now removes labels, reconnects retained parents, repairs a compaction boundary that names a removed label, and rebuilds the resolved labels in insertion order with their original winning timestamps. It preserves JSON member order and untouched member bytes through the existing field-replacement helper. | `core/session-manager.ts:1633-1738` | `TestCloneReconstructsLabelsAndRetainedChain`, `TestClonePreservesCompactionContextAtRemovedLabel`, `session/09-session-entry-wire` |
| W21 | The entry generator used eight random bytes instead of four, and entry producers used nanosecond formatting. Producers now use eight lowercase hexadecimal characters and three fractional ISO digits. Session UUIDv7 identities are unchanged. | `core/session-manager.ts:277-284,1202-1452` | `TestGeneratedEntryWireIdentity`, `session/09-session-entry-wire` |
| W23 | Print/JSON installed a SIGINT handler and returned numeric exit 130. They now leave SIGINT to the process default action, as Pi does. | `modes/print-mode.ts:49-64` | `TestModeWireLifecycle/print`, `/json`, strengthened `TestPrintModeSignalExitCodes`, `print/04-print-json-sigint-status` |
| W24 | A dead PTY produced an input EOF and flowed through normal CLI error exit 1. The input boundary now checks terminal viability on EOF and exits 129 on a dead-device error without terminal restoration. The redundant SIGHUP handler also misclassified a live terminal as dead; SIGHUP now uses the existing graceful process shutdown owner. | `modes/interactive/interactive-mode.ts:264-269,4145-4157,4179-4185,4231-4261` | `TestModeWireLifecycle/disconnect`, `/live-hup`, `session/10-terminal-disconnect-exit` |

D51 now names only interactive SIGINT restoration and numeric exit 130. It does not authorize print/JSON signal-status drift or dead-terminal exit drift. No divergence is added.

The base already fixes the built-in tool PascalCase detail leaks. Its `tools/15-builtin-tool-wire-details` compares complete results from the real published Pi factories, including sparse grep/find/ls details. A compiling mutation of `FindDetails.ResultLimitReached` back to `ResultLimitReached` makes that comparator fail. No duplicate tool production patch or RPC F1-F3 patch is included. Pi references: `core/tools/grep.ts:43-47`, `find.ts:43-46`, `ls.ts:25-28`.

## Red, green, and mutation evidence

Before production edits, the new Session tests fail on obsolete label rows, the unresolved compaction boundary, 16-character IDs, and nanosecond timestamps. The real-process probes report Pi `-2` versus PiG `130` for both active print and JSON SIGINT, Pi `129` versus PiG `1` for PTY disconnect, and Pi `0` versus PiG `129` for SIGHUP on a live terminal. Provider requests and positional extension command completion establish readiness; no sleep substitutes for those barriers.

The complete `get_entries` response records are compared, including `leafId`, all entries and fields, and array order. Generated entry IDs are injectively aliased after checking their exact format and uniqueness. Generated timestamps are checked for millisecond ISO precision before replacing the nondeterministic value. Imported entry IDs and timestamps are fixed and untouched. There are no scenario `normalize_replace` rules or substring-only acceptance checks.

All three new scenarios declare three runs. The session, print, JSON, and tools families pass against Pi after the fixes. The following compiling mutations each fail the associated unit or production-process regression and the canonical paired scenario:

| Mutation | Observed failure |
|---|---|
| Disable label filtering in `branchedSessionEntries` | Extra original label rows and unrepaired `firstKeptEntryId` |
| Restore the eight-byte entry generator | Initial `model_change.id` has 16 characters |
| Restore `time.RFC3339Nano` in the shared entry timestamp helper | Initial `model_change.timestamp` has more than three fractional digits |
| Restore the print/JSON SIGINT subscription | Both modes return numeric 130 instead of signal termination |
| Change the emergency terminal exit to 1 | PTY disconnect returns 1 instead of 129 |
| Restore PascalCase `FindDetails.ResultLimitReached` | Complete tool result differs at `details.ResultLimitReached` |

An existing test, `TestSessionContextEditRoundTripsPiSessionFilesByteForByte`, rejected the first clone implementation because re-marshalling changed JSON member order and escaped HTML characters. The source now uses `replaceJSONField`; the existing test is unchanged and passes.

The exact upstream regression `suite/regressions/8989-fork-compaction-label-boundary.test.ts` is ported and recorded in the test mapping. The broader label test file remains partial with missing cases enumerated. No pending test is silently promoted to complete.

## Repeat commands

Run with ambient worker Session/model overrides removed:

```sh
export PATH="/absolute/toolchain/bin:$PATH"
unset PIG_CODING_AGENT_DIR PI_MODEL PI_PROVIDER PI_SESSION_ID PI_SESSION_FILE
export PIG_PARITY_PI_BIN="$PWD/extensions/sdk-ts/node_modules/.bin/pi"
export PI_PACKAGE_ROOT="$PWD/extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent"
go build -o bin/pig ./cmd/pig
go test ./internal/codingagent -run 'TestClone|TestGeneratedEntryWireIdentity|TestSessionContextEditRoundTrips' -count=1
go test ./cmd/pig -run 'TestModeWireLifecycle|TestPrintModeSignalExitCodes' -count=1
make parity-family FAMILY=session
make parity-family FAMILY=print
make parity-family FAMILY=json
make parity-family FAMILY=tools
go vet ./...
GOOS=windows go vet ./internal/codingagent ./cmd/pig
go tool golangci-lint config verify
go tool golangci-lint run ./internal/codingagent ./cmd/pig
go run ./test/parity/cmd/gointerfaces -out test/parity/interfaces/pig-go.json
make coverage RESULTS=
make -k ci-contracts ci-drift
```

`RESULTS=` keeps regenerated coverage free of unrelated cached run claims. The four paired family logs retain the actual run evidence.

## Performance and lifetime scope

`BenchmarkSessionClone` clones 1000 messages with interleaved labels through the file-backed production path and removes each generated file. A two-second sample on the lane host measures 23.05 ms/op, 5,559,977 B/op, and 77,455 allocs/op. CPU and allocation profiles are retained with the run logs. This is a cost measurement, not a performance improvement claim or a resource bound for arbitrary Session sizes.

Clone reconstruction scans the selected path and the Session label history. It introduces no background work. The SIGHUP-specific listener, goroutine, and timeout are removed. The provider probe owns and joins its HTTP worker; the PTY probe owns only its subprocess group and descriptors. The new lifecycle tests prove active-provider SIGINT and idle-terminal disconnect/live-SIGHUP status, not active-tool process-tree cleanup or every extension shutdown case.

## Integration surfaces

Shared production edits: `coding/cli/main.go` (SIGHUP registration and signal comment), `coding/cli/print_mode.go` (remove SIGINT ownership), `internal/codingagent/session.go` (entry generation and timestamps), `session_manager.go` (clone and rename entry identity), `session_manager_projection.go`, `agent_boundary.go`, `interactive_commands.go`, and `interactive_extensions.go` (entry timestamp producers), `interactive_input.go` (dead-terminal input boundary), `interactive.go`, `interactive_signals_unix.go`, `interactive_signals_windows.go`, and `interactive_tui.go` (remove duplicate SIGHUP ownership and correct shutdown comments).

Shared evidence edits: `CHANGELOG.md` under the current integration release, `docs/parity/DIVERGENCES.md` D51, `docs/parity/PORT_MAP.md`, generated `AGENTS.md`/`test/parity/coverage.md`, generated `test/parity/interfaces/pig-go.json`, test mapping/policy, and the two current-disposition cells in `docs/parity/pending-tests-batches.md`. New tests, the Python real-process probe, and three scenarios are separate files.

The follow-up policy commit advances the frozen ported-test snapshot to the committed fix mapping, including the five already-ported files added since the previous snapshot and this lane's #8989 regression. It changes no hot-path tag. If integration cherry-picks the fix under a different SHA, rebind `baselineCommit` to that integrated commit and verify `baselinePorted` against its committed mapping.

## Gate limitations

The lane does not approve or mask baseline release blockers. `make -k ci-contracts ci-drift` reaches and reports the pending/partial hot-path test backlog, unapproved D78, and pre-existing private-infrastructure references in `test/parity/unit-evidence/fix-ext-ui-state.md` and `fix-node-scrollview.md`. Interface generation/drift, scenario lint, PORT_MAP drift, coverage drift, divergence consistency/guard, and documentation drift pass.

| Final check | Result |
|---|---|
| `go test ./internal/codingagent ./cmd/pig ./internal/codingagent/tools ./coding` | Pass |
| `go test ./test/parity/...` | Three baseline closure assertions fail because they expect all divergence rows to be provisional, while D78 is open. `TestImportCurrentDenominatorsAccountsEveryLedgerRow`, `TestDivergenceDashboardMatchesCurrentHeadingsAndScrutiny`, and `TestFoundationDashboardAccountsCurrentDenominatorsWithoutCredit` report this discrepancy. Other parity packages pass. |
| Four paired families: session, print, JSON, tools | Pass, declared scenario durability |
| Linux repository vet and Windows touched-package vet | Pass |
| Touched-package lint, `make lint-changed LINT_BASE=HEAD`, and `make lint` | Pass; no suppressions |
| `go fix -diff ./...` | Pass, empty diff |
| `make -k ci-contracts ci-drift` | Blocked by the baseline obligations above; no approval, hot-path tag, or committed baseline is weakened |
