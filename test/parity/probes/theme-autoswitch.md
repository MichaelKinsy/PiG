# Live automatic theme switching

Reference: Pi 0.87.1, upstream commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe`. This slice builds on `fix-color-detection`. The comparator is `extensions/sdk-ts/node_modules/.bin/pi`.

## Upstream contract

| Surface | Exact Pi source | Observed rule |
|---|---|---|
| Opt-in | `packages/coding-agent/src/modes/interactive/theme/theme.ts:581-610` | A setting is `lightTheme/darkTheme`, with one slash and two nonempty trimmed names. A single name stays fixed. |
| Initial detection | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:57-83`; `packages/coding-agent/src/modes/interactive/theme/theme.ts:709-732` | An automatic pair starts DSR 996 and OSC 11 concurrently. A scheme answer wins; otherwise the 100 ms deadline falls back to OSC 11, then COLORFGBG. An unset setting keeps the background-only detection and high-confidence persistence from the base slice. |
| Subscription | `packages/tui/src/tui.ts:877-941,1435-1456` | DSR is `ESC[?996n`. Notification opt-in/out is `ESC[?2031h` / `ESC[?2031l`. Pi sends these directly, including under tmux; it does not wrap them in a passthrough sequence. |
| Parsing and delivery | `packages/tui/src/terminal-colors.ts:28,67-73`; `packages/tui/src/tui.ts:1006-1012,1108-1118` | `ESC[?997;1n` means dark and `ESC[?997;2n` means light. A concatenated report takes its last scheme. Reports are consumed before listeners or focused components, even with no query or automatic setting. |
| Live transition | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:159-173` | Apply a changed selected theme immediately. A duplicate selected name does not invalidate the UI. There is no live-switch debounce, focus-triggered re-query, or settings write. The separate custom-theme file watcher has its own debounce and is not this protocol. |
| Preview | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:114-122,140-143` | Resolve a pair using the last terminal scheme. Preview invalidates and requests a render without changing the committed active name or disabling auto sync. A duplicate report therefore does not erase a preview. |
| Explicit extension selection | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:90-97`; `packages/coding-agent/src/modes/interactive/interactive-mode.ts:2573-2585`; `packages/coding-agent/src/modes/interactive/theme/theme.ts:506-524,790-815` | The name API disables auto sync before loading. It accepts a name, not an automatic setting. A missing name falls back to dark and returns failure without persisting the failed selection. A successful explicit selection survives a settings reload. |
| Lifecycle | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:51-55,129-133`; `packages/coding-agent/src/modes/interactive/interactive-mode.ts:841-884,4154-4165,4280-4307,4439-4449,6232` | Preserve automatic selection across renderer replacement, suspend/resume, and external-editor handoff. Disable notifications while the terminal is released and at shutdown. Reload awaits theme application. |
| Redraw cancellation | `packages/tui/src/tui.ts:932-934` | Stopping cancels the render timer. A stopped renderer must not dispatch a callback that became runnable concurrently. |

## Source fixes and regression evidence

1. PiG consumed scheme replies only through the settled startup detector, so it never enabled DEC 2031 or changed the live theme. `TestInteractiveAutomaticThemeNotifications` failed first with missing `ESC[?2031h` and `theme = dark, want light`. `TestInteractiveFixedThemeConsumesSchemeReports` also failed first because a report reached an extension shortcut. The input driver now owns the live controller, applies reports before editor/modal/listener delivery, and shares query handling across startup, settings, and reload.
2. Settings previews re-resolved pairs against COLORFGBG and forced a destructive repaint. They now use the last terminal scheme and ordinary invalidation. `TestInteractiveThemeStateTransitions` covers preview restoration, duplicate suppression, explicit opt-out, and retained selection. These assertions were added after implementation; the former preview path would restore dark despite the test's last light report.
3. The extension name API accepted a pair as a setting and preserved the previous theme on a failed name. The named path now matches the controller. The existing `TestExtensionThemeAPIsAreWired` assertion was corrected against the exact Pi implementation and a direct installed-Pi probe; it now checks the failure text and dark fallback, rather than requiring PiG's old incorrect behavior. `TestInteractiveThemeStateTransitions` rejects a pair passed as a name and checks immediate automatic opt-out.
4. The new shutdown regression exposed an existing race between `TUI.StopWithOptions` and the render timer. `TestTUIStopCancelsRacingRenderTimer` failed first with `stopped timer dispatched a render`. Stop now uses the same locked cancellation and generation invalidation as ordinary render cancellation. The initial lock change deadlocked the existing overflow path, which already owns the render lock. The final implementation shares a lock-held stop helper with that path. `TestOverflowDifferentialTerminatesWithCrashLog` and `TestPushedWidgetFrameFromOldWidthIsNeverPaintedAfterResize` both pass with the final code.

Additional guards:

- `TestTerminalColorSchemeReportParser`: dark/light, repeated reports, malformed/partial input, ordinary focus, and OSC input.
- `TestInteractiveThemeSettingQueriesWithoutBlockingModal`: a settings-driven query completes at its deadline while a modal is waiting for input, without blocking the owner loop or requiring a keystroke.
- `TestInteractiveThemePendingQueriesAndShutdown`: late OSC replies retain query order, a newer scheme answer wins, and shutdown drains the deadline worker and removes retained query state.
- `TestInteractiveThemeRebindsRenderer`: regular/fullscreen round trips disable then re-enable notifications and retain both live transitions.
- The base `TestInteractiveStockThemeDetection`, cancellation/error tests, and startup detector tests remain accepted guards.

`theme-autoswitch-pi.json` records the direct installed-Pi controller/parser probe. It shows no persistence on scheme changes, unchanged invalidation count for duplicates, preview retention, automatic opt-out after a failed explicit name, and rejection of a pair passed to the name API. The probe imports `dist/modes/interactive/theme/theme-controller.js` and `theme.js` from the installed coding-agent package, supplies a UI with recorded `invalidate`/notification calls and a light scheme answer, and calls `applyFromSettings`, dark/duplicate reports, `preview`, and `setThemeName` in the recorded order. Its parser comes from the coding-agent package's nested `node_modules/@earendil-works/pi-tui/dist/terminal-colors.js`.

## Terminal evidence

Both scenarios use the real Pi binary, an isolated tmux server, a resumed conversation, and `escaped_output_equal` for the complete conversation crop. No ANSI normalization or comparator weakening is used.

- `interactive-rendering/36-auto-theme-light-notification`: black initial background, `TERM=screen-256color`, then a light notification. Before the fix, PiG retained thinking foreground index 244 while Pi switched to 242.
- `interactive-rendering/37-auto-theme-dark-modal-notification`: white initial background, `TERM=tmux-256color`, `COLORTERM=truecolor`, then a dark notification while `/settings` owns focus. The final transcript matches Pi after leaving settings.

A compiling mutation that omits `applyTerminalTheme` delivery fails both scenarios: scenario 36 at 244 versus 242, and scenario 37 at RGB `108;108;108` versus `128;128;128`. The same mutation fails the automatic-notification, state-transition, and renderer-rebind unit tests. Each final scenario passes its three declared pairs.

The scenarios retain the base slice's exclusive scheduling because initial queries use Pi's fixed 100 ms deadline. Scenario 37's initial development barrier looked for a Theme row not shown on the first settings page. It now waits for the visible settings footer. The final escaped comparator and all deadlines remain unchanged.

## Resource and performance disposition

The existing terminal decoder remains the only input decoder. Live notifications add no worker, timer, extension IPC, Session decode, or transcript reconstruction. A settings query owns one deadline worker joined by the Run lifetime; completion and shutdown stop it. Query state is removed after settlement and its OSC reply. As in Pi's pending OSC queue, an unanswered query retains reply-order state until its late reply or mode disposal. Modal loops service query completions on the owner loop. Theme changes invalidate mounted components, as Pi does; ordinary editor input does not replay history.

`BenchmarkInteractiveAutomaticThemeNotifications` measures two opposite live notifications on the interactive controller path. One Linux/amd64 sample reports 649.0 ns/op, 109 B/op, and 4 allocs/op. CPU and allocation profiles were captured with `-cpuprofile` and `-memprofile`. This is a workload characterization, not a speedup claim.

## Verification

Passed on the final production code:

- `go test ./...` with the harness's ambient `PIG_CODING_AGENT_DIR` removed. This includes the extension conformance suite and the overflow caller regression.
- `go test -race ./internal/codingagent ./tui`.
- `go vet ./...` and `GOOS=windows go vet ./internal/codingagent ./tui ./test/parity/correspondence`.
- `go tool golangci-lint config verify`, touched-package lint, `make lint-changed LINT_BASE=1aba738c69`, and `make lint`.
- Complete `interactive-rendering`, `settings`, `fullscreen`, `selectors`, and `startup` parity families at declared durability.
- `make ci-contracts ci-drift`, with the Go interface inventory and static coverage regenerated.

The first full-suite command exceeded the tool invocation budget. A later full run exposed the lock-held overflow deadlock described above; the final complete suite passes without changing test deadlines. One concurrent correspondence snapshot collided with test-generated files and reported unstable Git object input. Contract snapshot gates run separately from source-generating tests. Coverage generation uses `RESULTS=` so it does not import unrelated transient run results.

The correspondence map now points the settings theme callback at `applyThemeFromSettings`, rather than the removed environment-only setter and forced repaint. Coverage promotes only the controller's existing PORT_MAP row. No divergence, lint suppression, parser override, retry, or production timeout change is added.
