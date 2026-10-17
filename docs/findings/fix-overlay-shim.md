Resolved: `/overlay-focus`, `/overlay-passive` and `/overlay-streaming` match Pi 1.3.0 (tmux scenarios `extensions-runtime/84-overlay-*`); overlapping `ctx.ui.custom` calls (`/overlay-stack`) stack as in Pi; see "Stacking and input ownership" below.

# fix-overlay-shim release handoff

## Stacking and input ownership (resolved)

Reference: Pi 1.3.0, `.upstream/v1.2.0/`.

- `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2966-3043` (`showExtensionCustom`) has no cross-call mutex. Each call awaits its factory and mounts its component: an overlay with `this.ui.showOverlay(component, options)`, an editor-slot component by clearing `editorContainer`, adding the component and calling `this.ui.setFocus(component)`. `done` calls `this.ui.hideOverlay()` for an overlay, or `restoreEditor()` (editor back in the slot, `setFocus(this.editor)`) for an editor-slot component.
- `packages/tui/src/tui.ts:729-742` (`showOverlay`) pushes an entry whose `preFocus` is the current focus and focuses the overlay unless it is non-capturing. `hideOverlay` (`tui.ts:829-841`) pops the entry and focuses the topmost visible overlay or the popped entry's `preFocus`. `setFocusInternal` (`tui.ts:602-665`) resumes a focused overlay whose restore an editor-slot component or dialog blocked once that component is unmounted.
- The TUI hands each key to its focused component only (`handleInput`), so of overlapping components only the one on top reads keys, and the one below gets the next key after the top one closes.

PiG before the fix: the subprocess UI bridge queued a second `ui.custom` call behind the first (`interactiveFocus`), and every mounted `RunRemoteOverlay` component held a lease on the one shared modal input channel, so two shown together competed for the same keys.

PiG now:

- `coding/extension/host/subprocess/ui_bridge.go` `handleCustom` opens every `ui.custom` call at once; only an already-cancelled call returns `cancelled` without mounting.
- `internal/codingagent/custom_overlay_control.go`: `customOverlay` is a `tui.Focusable`. The TUI calls `SetFocused` on the owner loop whenever focus moves to or from the component, and the component holds its modal input lease exactly while it has focus. A host-process component, dialog, or overlay shown over it takes the keys through TUI focus, and the lease returns when focus does. `retireInput` refuses a lease after the `RunRemoteOverlay` call returned. This replaces the `remoteCustoms` list and `syncRemoteCustomInput`.
- Host-process `ExtUIContext.Custom` already stacked through the TUI focus stack.
- The extension learns each later focus change: `RunRemoteOverlay` publishes it, in order with the component's input, through `extension.RemoteOverlayFocusHost`, and the bridge sends `ui.custom.focus` `{key, focused}`. Node, Go, Rust and Python queue it on the component's event queue, set the handle's `isFocused` and the component's focused flag (Rust components have none), and render the component again, as Pi's `setFocus` clears and sets `focused`.

Regression tests (red before, green after):

- `coding/extension/host/subprocess/ui_bridge_test.go#TestFocusedCustomComponentsStack` (rewrite of `TestFocusedOverlaysSerializeTerminalFocus`; replaces the probe `overlay-stack-host.go.txt` and the waiter-cancellation tests of the removed queue).
- `internal/codingagent/ext_ui_custom_stack_test.go`: `TestRemoteCustomOverlaysStackAndHandFocusBack` (replaces the probe `overlay-stack-input.go.txt`), `TestRemoteCustomOverlayStacksOverRemoteEditorSlotComponent`, `TestHostCustomOverlayStacksOverRemoteOverlay`; `TestRemoteCustomOverlayStacksOverHostOverlay` and `TestHostCustomOverlaysStackAndHandFocusBack` pin the paths that were already right.
- Focus publication: the same three host tests assert the `OnFocus(false)` and `OnFocus(true)` the component below receives; `test/extension-conformance/overlay_handle_test.go#TestCustomOverlayFocusChangesReachSDKs` (Node, Node packed, Go, Rust, Python, fused Go) asserts that each SDK renders `focused=false` and then `focused=true` from the handle.

Out-of-order close follows Pi 1.3.0: `close()` for an overlay hides it through its own handle (`overlayHandle?.hide()`, interactive-mode.ts:2999, Pi #10999), so closing the lower of two overlays removes the lower one and leaves the upper one with the focus and the keys. Until Pi 1.3.0 it called `this.ui.hideOverlay()`, which popped the topmost overlay whichever component closed. PiG's host-process and subprocess custom overlays hide their own handle (`TestHostCustomOverlayCloseOutOfOrderRemovesItsOwnOverlay`, `TestRemoteCustomOverlayCloseOutOfOrderRemovesItsOwnOverlay`); a factory's `tui.hideOverlay()` still pops the topmost overlay (`HideTopOverlay`).

## Factory TUI `showOverlay`

Reference: Pi 1.3.0 `interactive-mode.ts:3008` hands `showExtensionCustom`'s factory the TUI itself; `tui.ts:598-842` (`setFocus`, `showOverlay` and its `OverlayHandle`, `hideOverlay`). `packages/coding-agent/examples/extensions/overlay-qa-tests.ts` shows non-capturing panels with `tui.showOverlay` inside its factories and calls `handle.focus()` before the factory returns.

Before: the Node factory TUI had no `showOverlay`, so the three commands threw `this.tui.showOverlay is not a function` during construction.

PiG now:

- Node runtime (`runtime.mjs` `showChildOverlay`, `hideTopOverlay`, `setTuiFocus`; `overlay-handle.mjs`): the factory's `tui` has `showOverlay`, `hideOverlay` and `setFocus`. `showOverlay` asks the host synchronously (`ui.custom.showOverlay {key, overlayOptions}`), so the handle works at once, inside the factory, before the `ui.custom` call is sent. The panel's frames, input, focus notifications and handle controls reuse `ui.custom.render`, `ui.custom.input`, `ui.custom.focus` and `ui.custom.control` under its own key. Every answer that can move focus carries `focusedKey`, and the runtime clears the focused flag of the extension's other components, as Pi's `setFocus` does; a capturing `ui.custom` component does the same locally when it is shown. `tui.requestRender()` renders every component the extension shows, and a `ui.custom.focus` notification redraws them all.
- Host (`overlay_control.go`, `extension.RemoteTUI`, `ExtUIContext.ShowRemoteOverlay`/`HideTopOverlay`/`SetRemoteFocus`): a raw overlay goes on the TUI stack with no pending `ui.custom` call, gets its own input loop, and holds the modal input lease while it has focus (`SetFocused`). Its handle's `hide` removes exactly its entry (Pi's `OverlayHandle.hide`) and ends it; nothing waits on it. `tui.hideOverlay` pops the topmost overlay, and a `ui.custom` close still pops the topmost overlay. When the extension's connection ends (reload, shutdown, exit), the bridge closes each raw overlay, which removes its own entry.
- TUI (`tui/overlay_model.go` `restoreFocus`): a hidden overlay whose preFocus is a mounted overlay focuses it as an overlay (Pi's `setFocus(entry.preFocus)`), so its handle reports `isFocused()` and its own hide moves focus on. Checked against Pi 1.3.0's `TuiMainScreen`.
- Widget, header and footer factories: Pi hands them the TUI too (interactive-mode.ts:2432, 2537, 2568). `Runtime.factoryTui` gives every factory the same `tui`: `showOverlay`, `hideOverlay`, `setFocus`, `terminal`, and a `requestRender` that renders the factory's own surface and every component the extension shows (`TestNodeSurfaceFactoryTuiShowsOverlays`).
- Go, Rust and Python: their `Custom` APIs take a component, not a factory, and hand out no TUI, so no component of theirs can call `showOverlay`; the conformance row records them as exempt.

Probe, from the repository root with the Pi 1.3.0 Node dependencies present:

```sh
node test/parity/probes/overlay-shim.mjs
```

It runs the unmodified example's `/overlay-focus`, `/overlay-passive`, `/overlay-streaming` and `/overlay-stack` with the same keys on Pi (`showExtensionCustom` on Pi's `TuiMainScreen`) and on the production Node runtime, whose host calls a second Pi `TuiMainScreen` answers. After every step both must show the same overlays in order, rendering the same text and cursor markers, with the same one focused, and each PiG frame must match its component. Output, exit status 0:

```text
overlay-focus: 12 states equal, up to 4 overlays shown
overlay-passive: 7 states equal, up to 2 overlays shown
overlay-streaming: 12 states equal, up to 4 overlays shown
overlay-stack: 5 states equal, up to 3 overlays shown
```

Regression tests (red before, green after):

- `coding/extension/host/subprocess/runtime_node_overlay_handle_test.go#TestNodeFactoryTuiShowOverlayHandleIsSynchronous` (runtime).
- `coding/extension/host/subprocess/ui_bridge_raw_overlay_test.go`: `TestRawOverlayShowsAtOnceAndEndsWithItsHide`, `TestRawOverlayEndsWithItsConnection`, `TestRawOverlayWithoutTUIIsNotShown` (bridge).
- `internal/codingagent/ext_ui_custom_stack_test.go`: `TestRemoteRawOverlaysFollowPiShowOverlay`, `TestRemoteRawOverlayCloseRemovesOnlyItsEntry` (host TUI).
- `tui/overlay_state_test.go#TestHideRestoresFocusToAnOverlayPreFocus`.
- Conformance row `test/extension-conformance/overlay_show_test.go#TestFactoryTuiShowOverlayReachesTheHost`: Node isolated and packed; Go, Rust, Python and fused Go exempt.

Terminal scenarios (Pi 1.3.0 evidence in each file): `test/parity/scenarios/extensions-runtime/84-overlay-focus.toml` (focus cycling, typing, dismissal; `output_equal`), `84-overlay-passive.toml` (timer panel redraw while the controller keeps the keys; the timer value is normalized) and `84-overlay-streaming.toml` (panel focus cycling during streaming; stream times and counters are normalized). They run the byte-identical upstream example with quiet startup, so the startup header, which differs between the binaries, does not show behind the panels.
