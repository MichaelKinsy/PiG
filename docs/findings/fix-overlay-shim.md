KNOWN-GAP: `/overlay-focus`, `/overlay-passive`, and `/overlay-streaming` still fail because the Node factory TUI lacks `showOverlay`; `/overlay-stack` still queues its overlays and later notifications. Removing that queue alone exposes competing readers of one modal-input channel. No production fix is shipped.

# fix-overlay-shim release handoff

## Disposition

The task permits an explicit 0.3.x follow-up when a safe fix cannot fit the approximately 45-minute release timebox. This change records reproducible evidence and corrects the API matrix's completion claim. It does not approve an intentional divergence, close a parity row, or claim green behavioral acceptance. No production source, exported API, wire shape, SDK implementation, generated inventory, or accepted test changes.

Base: `6f400400610b433c3e5ac56b132a25e779588a4b` on the fix-overlay-shim branch. Probe date: 2026-09-28. Environment: Linux amd64, Go 1.27.1, Node 24.19.0. Reference: Pi 0.87.1. The source report is `staging/team/win/coordinator:COORDINATOR-STATUS.md`, entry `09-28 14:43Z`, citing public-021-native `75b1c4970`, `daily/overlay-show/`.

The capability is upstream-required extension/TUI substrate, not product behavior. A safe repair crosses the Node component runtime, host admission, native input routing, synchronous handle state, and lifecycle ownership. An admission-only patch compiled and passed the new admission probe but failed the native input-ownership probe. That patch was removed. Shipping it would exchange an obvious serialization failure for misrouted keystrokes.

## Measured upstream rule

All source references are under `.upstream/v0.87.1/`.

- `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2858-2936`: `showExtensionCustom` invokes the factory with the live TUI, awaits the factory, then mounts each overlay with `showOverlay`. It has no cross-call focus mutex. Each returned Promise waits for its own `done` callback.
- `interactive-mode.ts:2886-2898`: `done` calls `hideOverlay` in overlay mode, resolves the result, and disposes the custom component. It does not use the handle's targeted removal operation.
- `packages/tui/src/tui.ts:685-800`: `showOverlay` returns a synchronous handle. `hide` removes that entry, `hideOverlay` pops the last appended entry, `focus` changes focus and visual order, and non-capturing overlays do not acquire focus automatically. Hiding a raw TUI overlay does not itself call component disposal.
- `packages/coding-agent/examples/extensions/overlay-qa-tests.ts:115-155,264-298,939-1095,1250-1365`: the actual examples create overlapping custom calls and create raw child panels inside their factories. `/overlay-focus` calls `handle.focus()` before the outer factory returns. A Promise-shaped substitute for `showOverlay` is not compatible.
- Covering upstream tests include `packages/tui/test/overlay-non-capturing.test.ts`, `overlay-options.test.ts`, and `packages/coding-agent/test/interactive-mode-status.test.ts`. Their existing port dispositions are unchanged.

## Reproduce the examples

Run from the repository root after the normal Node dependencies are present:

```sh
node test/parity/probes/overlay-shim.mjs
```

The probe imports the exact example source through the existing Node loader. For the reference half, it invokes the installed Pi `InteractiveMode.prototype.showExtensionCustom` and Pi `TuiMainScreen`. It suppresses painting only; the reference mount, focus, handle, and dismissal methods run unchanged. It closes every reference component, including timer-owning examples. For the PiG half, it invokes the production `Runtime.openCustomOverlay` factory path. This is a constructor/lifecycle diagnostic, not terminal-output or all-SDK acceptance evidence. Replace this probe with real host conformance and paired terminal scenarios before closing the gap.

Observed output, exit status 1:

```text
Pi overlay-focus: mounted=4
Pi overlay-passive: mounted=2
Pi overlay-streaming: mounted=4
Pi overlay-stack: mounted=3 before any close
PiG overlay-focus: this.tui.showOverlay is not a function
PiG overlay-passive: this.tui.showOverlay is not a function
PiG overlay-streaming: this.tui.showOverlay is not a function
```

The mount expectations come from the example's three child-panel configurations plus its controller, or its single timer panel plus controller. They are not newly observed golden counts.

## Compile and replay both failing Go probes without editing production packages

The `.go.txt` files are diagnostic probe sources, not skipped tests or accepted passing evidence. Go's build overlay compiles them in their owning packages. They fail on the base and remain explicit blockers. This avoids committing a failing default-suite test or weakening the assertion to bless the bug.

```sh
python3 - <<'PY'
import json
from pathlib import Path
import subprocess
import tempfile

root = Path.cwd()
with tempfile.TemporaryDirectory(prefix="pig-overlay-probe-") as directory:
    overlay = Path(directory) / "overlay.json"
    overlay.write_text(json.dumps({"Replace": {
        str(root / "coding/extension/host/subprocess/overlay_stack_probe_test.go"):
            str(root / "test/parity/probes/overlay-stack-host.go.txt"),
        str(root / "internal/codingagent/overlay_stack_probe_test.go"):
            str(root / "test/parity/probes/overlay-stack-input.go.txt"),
    }}))
    raise SystemExit(subprocess.call([
        "go", "test", "-overlay=" + str(overlay),
        "./coding/extension/host/subprocess", "./internal/codingagent",
        "-run", "^(TestCustomOverlaysStackWithoutWaitingForFocus|TestOverlayStackProbeInputOwnership)$",
        "-count=1",
    ]))
PY
```

Both probes compile and fail behaviorally:

```text
TestCustomOverlaysStackWithoutWaitingForFocus:
  mounted 1 overlays before any closed; want back, middle, and front
TestOverlayStackProbeInputOwnership:
  back overlay still reads input=true; front reads input=true; shared channel=true
```

The first probe uses `testing/synctest` barriers and releases all three calls after measuring admission. The second mounts two overlays through `ExtUIContext.RunRemoteOverlay`, pumps the actual UI owner queue, checks the input routes, then closes and joins both calls. It bypasses the bridge's admission gate deliberately to test the native boundary that gate currently conceals.

The rejected experiment changed only `UIBridge.handleCustom` so `overlay:true` bypassed `interactiveFocus`. Results:

```text
TestCustomOverlaysStackWithoutWaitingForFocus: PASS
TestOverlayStackProbeInputOwnership: FAIL, same shared-channel failure
```

## Exact remaining work

1. Extend the factory TUI's live overlay capability in `coding/extension/host/subprocess/runtime-node/runtime.mjs`. Do not return an async handle where Pi returns a synchronous handle. A child created inside a factory must support immediate focus and visibility operations before the outer component mounts.
2. Define the mount/control/result ownership in `coding/extension/host/subprocess/protocol.go` before adding host or SDK methods. Reuse the existing remote component and layout mechanisms. Preserve targeted `OverlayHandle.hide` separately from append-tail `hideOverlay` and from `ui.custom` completion/disposal.
3. Fix input ownership before removing admission serialization. `internal/codingagent/custom_overlay_control.go:25-43` activates a reader for each overlay. `internal/codingagent/interactive.go:657-680` returns the same modal channel to every active lease. `internal/codingagent/ext_ui_context.go:486-505` drains that channel independently in each custom call. Focus changes must leave one ordered recipient, including hide, unfocus, editor-slot replacement, cancellation and restoration. No extension IPC may block the owner loop.
4. Publish authoritative handle state when one overlay changes another overlay's focus. `runtime-node/overlay-handle.mjs` currently retains local state and `ui.custom.input` refreshes the addressed overlay only. Do not let non-addressed handles or cached focused rendering silently retain stale state.
5. Preserve cancellation before and after mount, pre-bind render/close buffering, factory rejection, child removal, retained handles, connection loss, reload, and shutdown. Raw handle removal must not leave an orphaned host waiter. Component disposal must follow Pi's ownership rather than treating every raw overlay hide as `ui.custom` completion.
6. Exercise existing `Custom` calls across Go, Rust, Python and Node, isolated and packed cells, and fused Go. Add equivalent SDK capabilities if the protocol change exposes a new shared capability. A Node-only success is not cross-SDK completion.
7. Move the failing Go probes into normal regression tests as their behaviors are fixed. Add a conformance row proving concurrent admission, ordered input/focus restoration, result ordering, cancellation and cleanup. Add exact-output terminal scenarios for the four unmodified Pi commands, including focus cycling, dismissal, passive timer redraw and streaming redraw. Re-probe `extensions-runtime/54-node-overlay-handle` and the existing custom-component scenarios.
8. Coordinate with the pending Windows focus-marker change `55382c3ce`. It adds TUI `Focusable.SetFocused` propagation and may provide the focus transition mechanism needed here. It is not present in this base. Do not invent a competing focus tracker or claim that the marker fix alone routes remote input correctly.
9. Record representative render/input/resource measurements and run the requested vet/lint checks and relevant parity gates after the real fix. Regenerate runtime archives, API inventories and scenario-derived files when those sources change.

## Verification of this evidence-only handoff

The probes above are deliberately red and do not constitute a shipped fix. The raw local logs are under `/tmp/fix-overlay-shim-evidence/`; the source probes and observations in this change are the durable evidence.

| Command | Result |
|---|---|
| `node test/parity/probes/overlay-shim.mjs` | Exit 1; exact reference counts and all three PiG constructor failures shown above |
| `go test -overlay=… ./coding/extension/host/subprocess ./internal/codingagent -run '^(TestCustomOverlaysStackWithoutWaitingForFocus\|TestOverlayStackProbeInputOwnership)$' -count=1` | Both compile; both fail behaviorally on the base |
| Same Go probes with admission-only experiment | Host admission passes; native input ownership still fails; experiment removed |
| `go vet ./...` | Pass |
| `go tool golangci-lint config verify` | Pass |
| `make lint-changed` | Blocked: this integration history has no merge base with `main` |
| `make lint-changed LINT_BASE=<integration branch>` | Pass; no changed Go files relative to the task's actual base |
| `make lint` | Pass; 0 issues |
| Existing host overlay/focus regression subset | Pass; command below |
| Existing native custom-overlay regression subset | Pass; command below |
| `node --check test/parity/probes/overlay-shim.mjs` and `git diff --check` | Pass |

```sh
go test ./coding/extension/host/subprocess -run '^(TestFocusedOverlaysSerializeTerminalFocus|TestInteractiveDialogsWaitForFocusedOverlay|TestFocusedOverlayWaitCancelsWithoutTakingFocus|TestManyFocusedWaitersCancelWithoutLeakingFocus|TestNodeCustomOverlayPublishesMountedHandle|TestNodeCustomOverlayResolutionAndResize)$' -count=1
go test ./internal/codingagent -run '^(TestRemoteOverlayReclaimsInputAfterEditorReplacementUpstream|TestRunRemoteOverlayHonorsOverlayFlag)$' -count=1
```

No repository-wide `go test ./...` run, `make parity` run, performance profile, or all-SDK completion claim is made. No regeneration is required for this evidence-only change: it changes neither an API/CLI/settings surface nor a parity scenario or documentation mirror. The existing serialization tests passing does not prove Pi parity; the new probes expose their missing concurrent-overlay boundary.
