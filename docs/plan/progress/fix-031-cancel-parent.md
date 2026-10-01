# fix-031-cancel-parent: public issue #103

## Root cause

pi-mcp-adapter's `/mcp-adapter` calls `ctx.ui.custom(...)` inside `new Promise(resolve => …)` and drops the promise it returns (`commands.ts:760-771`). Esc runs `done(undefined)` then `resolve()`, so the command handler returns while the `ui.custom` host call is still outstanding (its `call_result` is a host round trip away). `Runtime.respond` (`runtime.mjs:3130` in 0.3.0) then calls `Connection.cancelParent(id)`, which rejects every host call still pending for that request. `openCustomOverlay` rethrows the rejection out of the promise nobody handles, and Node exits on the unhandled rejection: `packed member connection closed` then `packed process exited: exit status 1`.

Pi never cancels a host call when a command returns: `showExtensionCustom` resolves with `done`'s value once `done` ran and ignores later failures (`.upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2858-2940`, `if (closed) return` in the catch), and an overlay left open by a returned command stays open.

## Red

`TestNodeCommandReturnDoesNotCancelItsHostCalls` (`coding/extension/host/subprocess/dropped_host_call_test.go`), isolated and packed, two commands:
- `panel`: the pi-mcp-adapter pattern.
- `leave`: the command returns with its overlay open.

Red run, before any fix: `panel` fails in both modes with the issue's stack (`Error: host call cancelled with parent request r2 at Connection.cancelParent … Runtime.respond`) and `read failed: EOF` on the next command; `leave` fails in both modes with "the command's return closed the overlay it left open".

Base check: on the 0.3.0 hotfix base (409eaa4e1) the same red reproduces with the issue's stack (`cancelParent runtime.mjs:768 <- respond runtime.mjs:3130 <- handleRequest 2928 <- 1826`). Cherry-picked from 7c798543d (`-x`), citations re-pinned to Pi 0.87.1.

Audit of `cancelParent` callers (0.3.0 runtime.mjs): 660 (synchronous cancel, rejects `callSync`, which throws into the blocked handler's own stack, not a detached promise), 1797 (cancel envelope), 1865 (connection close), 3130 (respond). Only respond runs when Pi would still complete the call, so it goes. The cancel envelope (1797) and close (1865) reject a call the extension may have dropped, which is the same crash class: `TestNodeCancelledCommandDoesNotCrashOnDroppedHostCall` (red before the fix: `Error: host call cancelled with parent request r2` kills Node, `read failed: EOF` on the next command, in both modes).

## Green

- `Runtime.respond` no longer calls `cancelParent`: Pi settles a host call through its own completion, never because the command that started it returned (`.upstream/v0.87.1/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2858-2940`; `core/extensions/runner.ts:525-536` only wraps the call). Fixes `TestNodeCommandReturnDoesNotCancelItsHostCalls` (panel and leave, both modes).
- The rejection the runtime raises when the host cancels a request or the connection closes (cancel envelope, close, and the retained-context pre-check) is made by `hostCancelled` and forgiven by one `unhandledRejection` listener scoped to those errors (a `WeakSet`, so an extension cannot forge it). Any other unhandled rejection is rethrown (Error) or wrapped as Node's `ERR_UNHANDLED_REJECTION` (non-Error), so a real extension bug still exits Node with status 1: `TestNodeExtensionUnhandledRejectionStillCrashes` (guard; passes before and after, red under a swallow-all mutation). Fixes `TestNodeCancelledCommandDoesNotCrashOnDroppedHostCall`.
- Per-call marking of the runtime's own promise was rejected: `openCustomOverlay` and every other async `ctx` method rethrow the call's error from their own promise, which is the one the extension drops.
- The synchronous cancel (`callSync`, runtime.mjs 660) needs no change: its rejection is thrown into the blocked handler's own stack, never a detached promise.

## Mutation checks

- Restore `cancelParent` in `respond`: `TestNodeCommandReturnDoesNotCancelItsHostCalls` red.
- Drop the `hostCancellations` filter: `TestNodeCancelledCommandDoesNotCrashOnDroppedHostCall` red.
- Swallow every rejection: `TestNodeExtensionUnhandledRejectionStillCrashes` red.

## Second defect found under load (-race -count=24, GOMAXPROCS=4, taskset 4 cores, 4 CPU burners)

`TestNodeCommandReturnDoesNotCancelItsHostCalls/leave` timed out waiting for the overlay in 1 of ~96 runs. Root cause on the Go side: `Conn.readLoop` routes a command's response directly, while a call frame that preceded it waits in `inCh` for the host loop and its lane. The request goroutine then removes the request record, and `hostCallContext` (`conn.go`, `!pending` branch) treated the still-queued call as belonging to a cancelled request and dropped it without a `call_result`. Before the fix Node's `cancelParent` in `respond` masked the drop by rejecting the call. Red: `TestHostCallReadBeforeItsParentsResponseSurvivesTheResponse`. Guard for the kept behavior: `TestHostCallOfCancelledParentStaysCancelledAfterCleanup`.

## Third defect: the cancel frame could trail the call's own cancellation result

Under load, `Error: context canceled` (`code: ''`) killed Node in `TestNodeCancelledCommandDoesNotCrashOnDroppedHostCall`: `Conn.Request` cancelled the request's host calls, then queued the cancel frame, so a call's error result (`context canceled`, required by `TestParentCancellationCancelsBlockedHostCall`) could overtake the cancel and reject a dropped promise as a host error the runtime does not tag. A first attempt that suppressed the result (bbb05ab15) contradicted that existing test; it was reverted in the next green commit. Fix: queue the cancel first (`conn.go`, both the `cancelled` and `inactivityC` arms), then cancel the calls. Red: `TestParentCancellationQueuesTheCancelBeforeCancellingItsHostCalls`. The Node side then cancels the pending calls on the cancel and the late result finds none.

## Evidence

- Load: `go test -c -race` binary, `GOMAXPROCS=4 taskset -c 0-3`, 4 CPU burners, `-count=24` on the new tests plus `TestParentCancellation*` and `TestHostCall*`: PASS.
- `go test -race ./coding/extension/...`: green except `TestNodeVendoredTuiUpstreamTests/native-clipboard-linux` (needs Xvfb, absent on this host; unrelated). `./coding/pigletbuild ./coding/extension/pigsdk ./internal/experimental` race; `./internal/codingagent ./cmd/pig` green.
- `make parity-family FAMILY=extensions-runtime` and `extension-host`: ok.
- Not run: the real pi-mcp-adapter 3.3.0 in a TUI (the pinned red reproduces its exact stack with the same call pattern, `commands.ts:771`). Windows/macOS are not exercised here; the fixes are in platform-independent Node and Go code, `GOOS=windows go vet` is clean.
- Deferred: a per-provider wrapper of `Runtime.call` and the pre-check in `Runtime.call` are covered by the same tagged-error filter; no other deferral. No stubs for other families.
