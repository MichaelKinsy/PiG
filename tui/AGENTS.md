# TUI reliability

## Read first

- `../docs/project/CONTEXT.md`
- `../.upstream/current/packages/tui/src/tui-main-screen.ts`
- `../.upstream/current/packages/tui/src/components/editor.ts`
- `main_screen_upstream_parity_test.go` after confirming its currently documented Pi 0.84 model against the pinned source

## Contract

The Main Screen preserves scrollback. The Editor changes only the required visible rows. The Viewport follows Pi's state machine. Frame work does not scale with unchanged Session history.

## Failure modes

| Failure | Detection | Required result |
|---|---|---|
| Ordinary editor shrink repaints the viewport | Delete one newline or wrap row | Use a local differential update |
| The viewport snaps backward | Compare viewport origin before and after shrink | Match Pi |
| A frame replays transcript history | Compare short and large Sessions | Keep frame work bounded |
| A stale or duplicate row remains | Apply bytes to the terminal simulator | Match Pi's final screen |
| Scrollback is cleared | Detect `CSI 2J` and `CSI 3J` | Emit neither for an ordinary edit |
| The cursor homes | Detect `CSI H` | Use Pi-compatible relative movement |
| Wrapped input enters history early | Move Up within wrapped input | Keep Pi's cursor and history state |
| Resize corrupts layout | Change width and height with ANSI, wide text, and images | Match Pi's final screen |
| A hidden-row change cannot be repaired locally | Change content above the viewport | Use Pi's recovery render |
| A parent container serves a cached child that Pi would have redrawn | Mutate each exported component type beneath a parent after a first render, and compare the parent's frame with the component invalidated down its tree and rendered with no cache (`child_cache_contract_test.go`, which also fails on a mutant it is meant to catch) | The parent shows what Pi would draw; a component that embeds a `Container` answers through `Container.IsDirty`, and one without children invalidates itself in every mutator; a new exported component type, including one that gets `Render` by embedding or on a value receiver, has a fixture or a reason |
| A cursor or raw write lands among the bytes of a frame, or two goroutines are in the terminal writer | Park a frame in the writer and call `ShowOverlay`, `HideCursor`, `ShowCursor` or `WriteRaw` from another goroutine (`terminal_writer_ownership_test.go`) | Every write to the terminal holds the writer lock of the renderer (the alternate screen's enter, stop, colour-scheme reports and OSC 52 copy too, through `writeOwned`; `terminal_writer_lock_audit_test.go` fails for a function that writes to the writer before taking `outMu` and lists, with a reason, each one that holds it another way); a frame keeps it from its first write to its last; a write from a `Render` goes before the bytes of its frame |
| A component that embeds a `Container` and guards, with its own mutex, state that its children read is read without that mutex by the parent's frame (the promoted `IsDirty` scans the component's children: `LoginDialogComponent`'s settle scan read `d.input` and `d.secrets` while an OAuth flow's `ShowPrompt` wrote them) | `locked_container_guard_test.go` reads every non-test source and fails for such a type without a `Render` and an `IsDirty` that take its mutex (an exempt type names its reason); `login_dialog_lock_test.go` renders through a parent while a login goroutine mutates, under `go test -race` | Every read of the guarded state by a frame holds the mutex: the component overrides `Render` and `IsDirty` and calls `Container.IsDirty` under the lock |
| Code writes into the lines `Container.Render` or `Box.Render` returned (including a type that embeds a `Container` and edits the result of its own `Render`, and a component that edits another component's output) | `container_output_reuse_upstream_test.go` (Pi b7bb7ca905b8: the same slice while the children and their lines are unchanged), `container_render_cache_test.go` (capacity equals length), the guard tests that render every exported component twice, and the build tag `pigborrowcheck` (`borrow_check_test.go`) | The slice is borrowed and read-only, as Pi's `readonly string[]`: copy it (`slices.Clone`) before changing a line. Go cannot refuse an index write, so the default build has no check and no cost; `go test -tags pigborrowcheck ./tui` panics at the next render of the container or box when the lines changed under it, which catches an index write that changes a string and ends before that render, and cannot catch a write that is undone first or an array no container retains. A render replaces the slice, it never rewrites one it handed out; the capacity clip makes an `append` safe |

## Evidence

Define the terminal scenario before renderer code. Compare escaped Pi and PiG output and terminal-simulator state. Retain inputs, dimensions, bytes, screen state, and mutation failure. Use isolated tests for width, row, cursor, and viewport calculations. Profile editor shrink, streaming, overlays, and large transcripts. Run `go test -race ./tui` and the owning parity scenarios.
