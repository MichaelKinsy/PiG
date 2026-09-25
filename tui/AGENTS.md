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

## Evidence

Define the terminal scenario before renderer code. Compare escaped Pi and PiG output and terminal-simulator state. Retain inputs, dimensions, bytes, screen state, and mutation failure. Use isolated tests for width, row, cursor, and viewport calculations. Profile editor shrink, streaming, overlays, and large transcripts. Run `go test -race ./tui` and the owning parity scenarios.
