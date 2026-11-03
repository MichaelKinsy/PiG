# Interactive suspension

## Contract

Pi 0.87.1 `packages/coding-agent/src/modes/interactive/interactive-mode.ts:4278-4320` stops the TUI before sending SIGTSTP to its process group. It installs temporary SIGINT and SIGCONT listeners first. SIGCONT removes the temporary SIGINT listener, restarts the TUI, and requests a forced render. A failed suspend releases temporary ownership and propagates the error without restarting the TUI. Windows reports `Suspend to background is not supported on Windows` without suspending.

`internal/codingagent/suspend.go` owns the Go state machine. The input handler returns after signal delivery; an owned signal waiter schedules the later continuation on the UI loop. Its timer, signal registration, and temporary interrupt state are released before restart or error return. Cancellation drains the waiter through the mode's background-task scope and cannot restart a stopped owner. `interactive_signals_unix.go` supplies the real terminal and process operations. Suspension pauses and joins the existing stdin reader before restoring cooked mode, then resumes that same reader after raw mode is restored. The raw-input drain and theme-notification ownership are preserved across stop/start. Session and tool work are not canceled by suspension.

## Background resume (D227)

Pi's SIGCONT handler restarts the UI on any SIGCONT. PiG first compares `tcgetpgrp(stdin)` with its process group. A SIGCONT that reaches a background group (`bg`, or `kill -CONT` from another process) leaves the terminal untouched: PiG sends SIGTSTP to its process group again and its signal waiter keeps waiting. The SIGCONT that `fg` sends passes the check and restarts the UI as Pi does. A hung-up terminal answers the check with EIO, which takes the dead-terminal exit (129); a stdin terminal that is not the controlling terminal answers ENOTTY and restarts as in Pi. Raw-mode entry and exit retry EINTR as libuv's `uv__tcsetattr` does, so a stop and continue inside the ioctl does not fail it.

## Signal ordering

The temporary SIGINT listener is a userspace dispatch rule, not kernel SIG_IGN. When SIGINT is dispatched before continuation cleanup, it is ignored. After continuation cleanup, SIGINT follows the normal termination path. Simultaneously pending SIGINT and SIGCONT therefore have no guaranteed survival outcome. D51 owns PiG's numeric exit 130 and terminal restoration; this change does not expand that divergence.

## Evidence

- `TestInteractiveSuspendUpstream` ports the three original Windows, resume, and failure cases. It calls the handler synchronously, inspects its returned state, and only then invokes its captured SIGCONT callback. It preserves the keep-alive interval, stop/start/render ordering, and error propagation.
- `TestPairReview4SuspendReturnsBeforeContinue` guards the original return-before-callback boundary.
- `TestSuspendCancellationCleansUpWithoutRestart` checks cancellation cleanup without restart or repaint.
- `TestSuspendContinuationUsesOwnerLoopAndSurfacesErrors` and `TestSuspendShutdownJoinsContinuationWithoutRestart` drive real SIGCONT registration, owner-loop delivery, queued-continuation cancellation, and background-task draining.
- `TestSuspendPausesInputBeforeRestoringTerminal` checks the current terminal reader's handoff before cooked-mode restoration.
- `TestSuspendKeepsSignalDeliveryEnabled` rejects kernel-level SIG_IGN.
- `40-suspend-resume-session` runs a real foreground job in a private tmux session. It waits for a completed provider turn, enters a draft, checks stopped process state, resumes that job with `fg`, and submits the retained draft. It compares the complete escaped response row.
- `43-suspend-signal-order` uses real SIGINT and SIGCONT with acknowledged armed/ignored/render boundaries. Only the stop syscall is replaced. It checks both dispatch orders without timing assumptions.
- Compiling mutations that omit the stop call or discard the suspend error fail the upstream operation tests.
- `TestSuspendedSessionResumedInTheBackgroundStaysParkedUntilFg`, `TestSuspendedSessionWhoseTerminalIsGoneDoesNotRestart` and `TestSuspendWaiterKeepsWaitingAfterAParkedContinuation` cover the D227 parking, the dead-terminal answer and the waiter that survives a parked continuation. `TestStdinInForegroundFollowsTheTerminalProcessGroup` runs the `tcgetpgrp` check against a real pseudo-terminal as the foreground group, as a background group and after a hang-up, and `TestStdinInForegroundTreatsATerminalThatIsNotTheControllingTerminalAsForeground` covers ENOTTY. `TestEnterRawModeRetriesAnInterruptedTermiosIoctl` and `TestRestoreTerminalFromSignalRetriesAnInterruptedIoctl` cover the EINTR retry.
- `BenchmarkSuspendLifecycle` measures the owned ticker and callback lifecycle. It does not claim whole-terminal latency or a performance improvement. Profiles belong with the lane evidence.

The native Windows process path requires Windows execution. Cross-compilation and the injected Windows state-machine test do not substitute for that qualification.
