# Lane port-99-f5: tui family (upstream 0.87.1 to 0.99.1)

Base `porter/pi-0.99.1` (40001cd42). Scope: plan Phase 2 step 5 (packages/tui) and the `tui-components`, `autocomplete`, `fullscreen`, `clipboard-images` families.

## Environment

- The npm release-age cooldown was not in force on this host (no `~/.npmrc`, `npm config get min-release-age` is null). A plain `npm install --ignore-scripts` of the 0.99.1 coding-agent package in `/tmp/pi99` succeeded, so no override was used. The installed `pi` reports version 0.99.1 and is the comparator for probes; nothing was written under `~/.pig`.
- Upstream tui tests run directly with Node 24 type stripping in `/tmp/pi99-tui` (copy of `.upstream/v0.99.1/packages/tui`, dependencies linked from the install, `@xterm/headless@5.5.0` added). Used as an oracle for color math and wheel-scroll values.
- Tests run with `PATH` including `~/.local/share/mise/installs/node/24.19.0/bin` (the mise `node` shim fails under a temporary `HOME`) and temporary `PIG_CODING_AGENT_DIR`, `PI_CODING_AGENT_DIR`.

## Red stage

Ported tests, upstream citations in each test:

| upstream test | Go test |
|---|---|
| `tui/test/colors.test.ts` (4) | `tui/colors_upstream_test.go` |
| `tui/test/wheel-scroll.test.ts` (6) | `tui/wheel_scroll_upstream_test.go` |
| `tui/test/visible-width.test.ts` (4) | `tui/widthx/visible_width_upstream_test.go` (passes already: behavior-neutral fast path) |
| `tui/test/terminal-colors.test.ts` (new: `parseOscColorResponse`, `queryTerminalColors` x3) | `tui/terminal_colors_query_upstream_test.go`, `internal/codingagent/terminal_colors_query_upstream_test.go` (InteractiveMode input boundary) |
| `tui/test/autocomplete-skill-slash.test.ts` (5, replaces the old combined Go case) | `tui/autocomplete_wrappers_upstream_test.go` |
| `tui/test/autocomplete.test.ts` (4 new) | `tui/autocomplete_wrappers_upstream_test.go` |
| `tui/test/editor.test.ts` (2 changed loops) | `tui/editor_completion_upstream_test.go`, `tui/editor_autocomplete_boundary_upstream_test.go` |
| `tui/test/mouse-components.test.ts` (1 new) | `tui/mouse_components_test.go` |
| `tui/test/overlay-options.test.ts` (2 new) | `tui/overlay_options_upstream_test.go` (pass at red, see below) |
| `tui/test/terminal-image.test.ts` (truecolor + 8 sizing) | `tui/terminal_image_sizing_upstream_test.go` |
| `tui/test/terminal.test.ts` (1 new) | `tui/terminal_negotiation_upstream_test.go` |
| `tui/test/tui-alt-screen.test.ts` (1 new) | `tui/tui_alt_screen_wheel_upstream_test.go` |
| `coding-agent/test/clipboard-paste-file-paths.test.ts` (6 cases, new) | `internal/codingagent/clipboard_paste_file_paths_upstream_test.go` |
| `coding-agent/test/clipboard-image.test.ts` (2 new, 3 changed expectations) | `internal/codingagent/clipboard_image_upstream_test.go` |
| `coding-agent/test/clipboard-image-native-errors.test.ts` (changed) | `internal/codingagent/clipboard_image_native_errors_upstream_test.go` |

Removed with their upstream tests: the old combined `TestSlashOnlyProvider_SkillCommandFilter` (its `!contains deep-research` assertion is the 0.87.1 behavior that upstream 0.99.1 reverses).

Signature-only stubs added in this commit: `tui/colors.go`, `tui/wheel_scroll.go`, `TerminalColors`/`OscColorReply`/`ParseOscColorResponse` and `QueryTerminalColors`/`ConsumeTerminalColorResponse` (also on `Renderer`), `TuiAltScreenOptions.WheelScrollLines` as `WheelScrollLines`, `SetWheelScrollLines`, `CalculateImageCellSize` variadic `optimizeAspectRatio`, `renderedImage.Columns` (existing upstream field, now populated), `NativeClipboard.GetFilePaths`, `readClipboardFilePaths`.

Red run (`go test ./tui/... ./internal/codingagent` for the touched tests): 72 failing subtests, all new behavior; every other test in `./tui/...` passes. Failure counts by area: colors 4, wheel scroll 6 plus the alt-screen runtime update, OSC parse and query 4, skill slash 3, wrapper autocomplete 2 plus 2 sub-cases, editor debounce 16, submenu mouse focus 1, Kitty sizing 3, `-direct` truecolor 1, DA1 forwarding 1, clipboard X11 probing 6, native error display 1, file paths 6, input boundary 3.

Cases that pass at red, with reason:
- `visible-width.test.ts` (4): upstream changed only the implementation speed; the Go width code already returns the same values.
- `overlay-options.test.ts` hiding after stop (2): the Go overlay lifecycle never writes a cursor-hide outside a render, and `Stop` shows the cursor. Mutation check: adding `t.HideCursor()` to `hideOverlay` makes the first case fail; reverted.
- `editor.test.ts` "does not auto-trigger" with `foo(@src`, `keeps wrappers that are closed inside the path`, iTerm2 sizing (2), Kitty ceiling case: guard the behavior that must not change.

Per-case substitutions (L1): the Go renderer has no `TuiMainScreen` input dispatch, so the `queryTerminalColors` cases run at the renderer seam (`ConsumeTerminalColorResponse`) and again through `InteractiveMode.dispatchKey`, as the 0.87.1 background-query cases did. Go's `SettingsList` reports a value change through `ChangedID` and has no `onChange`, so the submenu pick is recorded in the submenu's done callback. Bash-mode paste follows a `!` prefix because Go derives bash mode from the editor text where the upstream mock sets `isBashMode`.

## Deferred to family 9 (theme system)

`QueryTerminalBackgroundColor`, `ConsumeOsc11BackgroundResponse`, `ParseOsc11BackgroundColor`, `IsOsc11BackgroundColorResponse` and their old tests stay until family 9 replaces the startup theme detection (`startup_ui.go`, `presentation_theme.go`, `ext_ui_context.go`); upstream removed them with the theme controller.

## Green stage

Commits: red 806463312, green 8d3c50de0, guard tests 7fde36e65. Ported test files are unchanged since the red commit. PiG-authored tests that asserted superseded behavior were adjusted (DA1 ownership: `raw_mode_test.go`, `terminal_negotiation_stop_test.go`, `negotiation_leak_test.go`; X11 `TARGETS`: `clipboard_test.go`, `clipboard_linux_test.go`; paste errors shown: native-errors case).

Implemented (each cites the upstream 0.99.1 file in its tests): `tui/colors.go`, `tui/oklab.go` (bit-exact against upstream over 2,600 seeded inputs, `tui/testdata/colors-oracle.*`), `tui/wheel_scroll.go` and alt-screen wiring, `QueryTerminalColors`/`ConsumeTerminalColorResponse`, DA1 ownership in `tui/terminal.go` and `terminal_input.go`, path and `@` wrapper autocomplete, editor trigger boundary, `/skill` matching, Kitty aspect-ratio cell counts, `TERM=*-direct`, `GetTerminalColorMode`, forwarded-mouse focus in `DispatchMouseEvent`, settings submenu mouse, select-list click, Box unpadded cache, `NativeClipboard.GetFilePaths` (darwin), `readClipboardFilePaths`, paste of file paths and shown paste errors, X11 `TARGETS` handling.

Verification: `go test ./tui/...` and `./internal/codingagent` pass; lint 0 issues for `./tui/...`; `GOOS=windows` and `GOOS=darwin` vet clean; about 30 mutations of the key fixes all fail the tests (helper `/tmp/f5/mut.sh`); two extra guards were added where a mutation first survived (reassembled DA1, submenu focus). Load: `-race -count=24`, `GOMAXPROCS=4`, `taskset -c 0-3`, 12 CPU burners: query, wheel, negotiation, autocomplete, editor, Box, settings submenu and the clipboard/paste/query boundary tests all pass. `internal/nativeplatform` has three failures that need `Xvfb`, which is not installed on this host.

Not ported, with reason: Markdown token reuse (the Go renderer is line based and has no token stage); `visibleWidth` fast path (already present in `widthx`). Not run here: the darwin pasteboard test (compiles only).

Isolation note (lesson 17): a number of `go test` runs used the real HOME, so extension-host tests wrote caches under `~/.pig` (`state/pigsdk`, `cache/cells`, `cache/node-compile`). No credentials were read. All later runs use a temporary HOME, PIG_HOME and agent dirs.

`./cmd/pig` under an isolated HOME: `TestEmbeddedPackedAuthDiscoveryAndLoginByLanguage` and `TestExtensionInitScaffoldsBuildAndRegister` fail identically on base `40001cd42`. The Python fixture cannot start because the mise Python shim fails without the real HOME. This is a limitation of the isolated environment, not a change in this lane. The lane touches no `cmd/pig` code.

## Parity against installed upstream 0.99.1

The runner refuses any upstream other than the pin, so the four families ran from a scratch copy with the pin and fixture version strings set to 0.99.1 and the installed 0.99.1 package as the comparator. Nothing in the branch moves the pin.

- `clipboard-images`: every non-deferred scenario passes (6 pass, 2 skipped as declared).
- `tui-components/15-terminal-background-query-lifecycle`: re-derived. Upstream removed the OSC 11 parser and query, so both fixtures now drive `queryTerminalColors` with one fixed OSC 10/11/4 palette (D-D). The output is byte-identical between PiG and upstream 0.99.1. This is committed (`4834f2e58`).
- Open, `tui-components/14-terminal-negotiation-framing`: the upstream fixture calls `queryAndEnableKittyProtocol()`, so the DA1 replies in its `zero` and `DA` steps are owed and consumed. The PiG fixture builds a `TerminalInput` on a terminal that never issued the query, which now forwards those replies (the behavior the ported unit tests require). The PiG method is unexported (a ported test calls it by that name) and its caller needs a tty, so the fixture cannot set the state without a new exported hook. Left for the lead to decide; not guessed.
- Not this lane (theme, header and command-count differences; the default theme colors, the built-in command list and the system prompt changed upstream): `tui-components` 02, 16, 21, 22; `fullscreen` 01, 02, 10; `autocomplete` 01 (25 vs 24 slash commands), 09, 10, 11. Their diffs are color values and counts, not tui behavior.
- `TestParityFixtureProgramsCompile` fails on `test/parity/testdata/models-runtime-go` (`ai.AnyModel`), which belongs to the ai families.
- No new scenario covers colors, wheel auto scroll, autocomplete wrappers, Kitty stretching or file-path paste. The upstream-derived unit tests and the bit-exact colors oracle (`tui/testdata/colors-oracle.*`) cover them; a scenario pair for wheel auto and Kitty stretching is follow-up work.

Generated files were regenerated in `d48871f88` (`pig-go.json`, recommendations). They are drift-clean for this tree.

## Go API changes (pre-1.0, no shim per L14)

Migration notes for callers of the `tui` package. In-tree callers are updated in this lane.

- `tui.TuiAltScreenOptions.WheelScrollLines` changes from `int` to the struct `tui.WheelScrollLines{Auto bool; Lines float64}`, because upstream widened `number` to `number | "auto"`. Replace `WheelScrollLines: n` with `WheelScrollLines: tui.WheelScrollLines{Lines: float64(n)}`. The zero value still moves one line. `TuiAltScreen.SetWheelScrollLines` is new.
- The `tui.Renderer` interface gains `QueryTerminalColors` and `ConsumeTerminalColorResponse`. Out-of-tree `Renderer` implementations must add both.
- `tui.CalculateImageCellSize` gains a trailing `optimizeAspectRatio ...bool`. Existing calls compile unchanged; a variable of the old function type does not.
- `tui.TerminalColorQueryOptions` gains `OnLateReply`, and `tui.NativeClipboard` (`nativeplatform.NativeClipboard`) gains `GetFilePaths`. Keyed composite literals are unaffected; unkeyed ones must add the field.
- Behavior: `ProcessTerminal` input forwards a DA1 reply that its own keyboard protocol query does not owe. Raw input consumers can now receive `ESC [ ? … c`, as upstream's do.

## Review rev-port-99-f5

Fixes on top of the lane: settings submenu mouse returns the submenu's own result (settings-list.ts:180-183), the `Box` render cache is keyed on its exported padding, a guard for duplicate color-query targets, and `tui-components/14-terminal-negotiation-framing` runs the keyboard protocol query on the PiG side (now byte-identical to the Pi fixture). Still open: `tui-components/15-terminal-background-query-lifecycle` needs the 0.99.1 comparator, so it fails against the pinned package until the pin moves, and its Pi fixture's version guard must change with the pin.
