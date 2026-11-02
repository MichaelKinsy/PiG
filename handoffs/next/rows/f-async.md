# Lane F async-contract row (exec.ts), delivered by FollowF

`test/parity/async-contracts.toml` row for `packages/coding-agent/src/core/exec.ts` (disposition `ported`):

```toml
[[files]]
path = "packages/coding-agent/src/core/exec.ts"
disposition = "ported"
contracts = ["awaited", "cancellable", "detached-owned", "ui-handoff", "microtask-ordering"]
evidence = [
  "coding/extension/exec_oracle_test.go#TestExecCommandMatchesPi",
  "coding/extension/exec_max_buffer_test.go#TestExecCommandCapsCombinedOutputAtMaxBuffer",
  "coding/extension/exec_max_buffer_test.go#TestExecCommandCountsStdoutBeforeStderrWhenTheChildWroteItFirst",
  "coding/extension/exec_utf8_chunks_test.go#TestExecCommandDiscardsPendingUTF8WhenAStreamExceedsTheCap",
  "coding/extension/exec_tree_test.go#TestExecCommandForceKillsAChildThatIgnoresSIGTERMAfterFiveSeconds",
  "coding/extension/exec_signal_unix_test.go#TestExecCommandOptionsSignalEndsTheCommand",
  "coding/extension/exec_timeout_unix_test.go#TestExecCommandStartsTheTimeoutAfterTheSpawnAsPiDoes",
  "coding/extension/exec_drain_test.go#TestExecCommandKeepsTheOutputOfAChildThatExitedBeforeItsReadersRan",
  "coding/extension/exec_test.go#TestExecCommandReportsInitiationBeforeCompletion",
  "test/extension-conformance/exec_abort_test.go#TestExecAbortAcrossSDKs",
  "test/extension-conformance/exec_limits_test.go#TestExecLimitsAcrossSDKs",
]
rationale = "..."
```

## Contracts
- **awaited**: `execCommand` is an async function whose promise settles once `waitForChildProcess` resolves (the child exited and its pipes ended or idled 100 ms) and the timers and abort listener are cleared; a failed spawn resolves `{code: 1, stderr: error.message}` instead of rejecting, and an invalid `maxBuffer` rejects with a RangeError before any spawn. Go: `ExecCommand` blocks until the same point and returns `(ExecResult, error)`; the only error is the RangeError or Node's thrown spawn argument error.
- **cancellable**: `options.signal` (listener removed at settle), `options.timeout` (a timer started after the spawn) and the `maxBuffer` overflow all call one `killProcess`: SIGTERM, then SIGKILL after 5 s if `exitCode`/`signalCode` are still null (the timer is cleared at settle). Go: ctx cancellation, `options.Signal` and `context.WithTimeout` drive one `killProcess` under a mutex; `time.AfterFunc(execForceKillDelay)` sends SIGKILL and is stopped at settle.
- **detached-owned**: the stream `data` callbacks, the abort listener and the two timers run on the event loop beside the awaited promise and are all torn down before it resolves. Go: the childwait reader goroutines (one per pipe, `awaitTurn`-ordered so stdout is counted before stderr as Node's loop does), the ctx watcher goroutine and the force-kill timer are owned by `ExecCommand`, joined (`pipes.Wait`, `<-watchDone`, `Timer.Stop`) before it returns; nothing outlives the call.
- **ui-handoff**: pi.exec is called from an extension handler on the host's ordered call lane; the synchronous prefix (spawn attempted) completes before the next call is applied, then the command completes independently. Go: `CallInitiated(ctx)` after the spawn attempt, so a subprocess host advances the extension's ordered lane while the child runs.
- **microtask-ordering**: results are observed in the order of the output events: `takeWithinCap` counts chunks as the loop delivers them, so with one cap over both streams a child that wrote stdout first keeps stdout. Go: childwait's `awaitTurn` makes a later pipe wait for an earlier pipe's readable or taken output (poll(2) on Unix, `PeekNamedPipe` on Windows, type-checked only), and `onData` calls never overlap.
- `concurrent-joined` does not apply: exec.ts has no `Promise.all`/`race`.

## Rationale text for the toml row
exec.ts at e7a0df27 is one awaited promise around a spawn: stream `data` listeners feed a byte cap that kills the child and destroys the pipes on overflow, an abort listener and a timeout timer share `killProcess` (SIGTERM, then SIGKILL after 5 s if the child has not exited), and `waitForChildProcess` settles the call once the child exited and its pipes ended or idled; Go keeps one blocking `ExecCommand` that owns a reader goroutine per pipe (childwait, stdout turn-ordered before stderr), a ctx watcher and a `time.AfterFunc` force-kill timer, all joined or stopped before it returns, with `CallInitiated` marking the end of the synchronous prefix for the host's ordered call lane.
