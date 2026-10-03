# fix-992-stdin-eof progress

Slice: RPC stdin-EOF ordering races on the 0.4.0 tip (upstream 0.99.2).

## Red run (tests only, no stubs needed: the tests compile against existing helpers)

Load recipe: `taskset -c 150-151 GOMAXPROCS=2` plus 4 pinned CPU burners, `-test.count=30`, `cmd/pig` test binary; Pi rows never failed.

| test | unloaded | loaded (30 runs) |
|---|---|---|
| `TestRPCInputEndAfterCommandNotificationAnswersComparedWithPi` (parity 36 shape: notify, client closes stdin) | red | 29/30 runs fail (pig 21, pig-with-sibling 25 of 30) |
| `TestRPCInputEndResponsePrecedesShutdownNotificationComparedWithPi` (pig/sync, nexttick, micro, immediate, microimmediate; sibling too) | passes | 13/30 runs fail |
| `TestRPCCommandAfterAgentEndAnswersAfterAgentSettledComparedWithPi` (parity 26 shape with an extension agent_settled handler) | red every run | n/a |
| parity `36-model-registry-session-manager` (runs=1, load) | | 7/30 |
| existing `TestRPCInputEndAfterExtensionCommandComparedWithPi/pig*` | | about 25% of runs, row varies |

## Green evidence (taskset -c 150-151, GOMAXPROCS=2, own burners, pids killed by the load script)

- Before / after, 30 loaded runs of each ported test: `…AfterCommandNotificationAnswersComparedWithPi` 29 -> 0, `…ResponsePrecedesShutdownNotificationComparedWithPi` 13 -> 0, `TestRPCCommandAfterAgentEnd…` (extension and no-extension, 40 rounds) red every run -> 0. Parity `36-model-registry-session-manager` loaded, 60 runs: 7/30 -> 0/60. stdin-eof-b measured the same tip at 0/300 (real-process rows) and 0/75 (`/wait`).
- Parity `26`: the loaded failures (21/60) are all `/15/result/structuredContent/wall_time_seconds: pig=0 pi=0.1`, Pi's bash tool timing, not ordering; I did not alias it. The ordering race of 26 is covered by `TestRPCCommandAfterAgentEndWithoutExtensionsAnswersAfterAgentSettled`.
- Mutations (each fails a named test): no settle wait in the command loop (`TestRPCCommandAfterAgentEnd*`); no `windowIdle` wait (`TestRPCInputEndResponsePrecedes…`, 4/10 loaded); eager suspension report (`TestRPCInputEndAfterCommandNotification…` 10/10 loaded, three `TestNodeRuntimeReportsCommandSuspensionAfterInputEnd` rows); no fresh window (`…resumed onto a short call…`); `rpcSettleGate` tail/compaction mutations; `windowIdle` always closed; `inputEnd` without settle (`TestRPCShutdownInputEndSettlesBeforeShutdownStarts`).
- Regression found and fixed on the way: `TestRPCInputEndQuarantinedNodeCommandComparedWithPi/child_process/pig` failed 6/15 loaded (baseline 0/15) because a Node suspension report waits for an earlier exec; the window count now follows the call frame (`TestCommandWindowCountsNodeCommandWaitingOnAHostCall`). After that fix this row, `…AfterCommandNotification…` and `…ResponsePrecedes…` passed twice unloaded; I did not repeat the loaded run (lead stopped the full-suite comparisons).
- Gates: build, vet, `GOOS=windows go vet`, gofmt, `go fix -diff` (empty), golangci-lint on `./cmd/pig/...` and the subprocess package (0 issues), `check-public-claims.py` ok. `go test -race` of `./cmd/pig` and the subprocess package: no DATA RACE; the failures are Python/Rust/Xvfb toolchain tests that cannot run under my temporary HOME, plus `TestExtensionVirtualModelFlushFailureStopsStartup` and `TestInteractiveExtensionHostWiringMatchesHeadlessModes` in `cmd/pig`, which I did not investigate (joint-run-6's hourly full run will show whether they are environmental).
- Review fixes (stdin-eof-b findings 1-4): docs/extension-api-parity.md and the `NotifyPayload` comment rewritten, D19 markers, direct unit tests, the timer edge row, and the settle wait moved into `rpcShutdown.inputEnd` with a deterministic ordering test.
- Open: the unobservable tail-wait limit needs a numbered divergence (QUESTION in questions/fix-992-stdin-eof.md); `/wait` + immediate EOF still differs from Pi (Pi answers, Pig writes nothing) because `waitForIdle` counts as a closing call even on an idle Session.
- Tests changed (not ported upstream tests): `command_drain_isolated_test.go` and `command_drain_process_test.go` call `EndInput` before `waitCount(1)` because a command's suspension is now reported after `runtime_input_end`; `fakeSuspendSource` gained the window method.
