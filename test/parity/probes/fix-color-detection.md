# Stock terminal color investigation

Reference: Pi 0.87.1, upstream commit `f07218c4d4bbc12bef056a7058c3dd49dfe41abe`. The installed comparator is `extensions/sdk-ts/node_modules/.bin/pi`. The Node matrix checks the installed package version against `coding/pigversion.UpstreamVersion` before comparing behavior.

## Findings

| Surface | Pi rule and source | Result |
|---|---|---|
| Capability detection | `packages/tui/src/terminal-image.ts:69-158` checks tmux/screen before terminal identities, recognizes truecolor hints and modern terminals, and applies `PI_TRUE_COLOR` last. | No difference in the 493-case matrix. It includes Linux, macOS and simulated Windows, Apple Terminal, iTerm2, Windows Terminal, VS Code, JetBrains, Ghostty, kitty, WezTerm, Alacritty, Warp, Zed, SSH, empty/16-color/256-color TERM values, CI, NO_COLOR and FORCE_COLOR. |
| Theme color mode | `packages/coding-agent/src/modes/interactive/theme/theme.ts:528-529` chooses only truecolor or 256color. `theme.ts:178-244` performs the weighted, saturation-preserving palette conversion. | No difference for either built-in theme's authored color tokens. Pi does not have a 16-color or no-color theme mode. NO_COLOR and FORCE_COLOR affect Chalk separately, not theme color escapes. |
| Stock theme selection | `packages/coding-agent/src/modes/interactive/theme/theme-controller.ts:57-83` queries OSC 11 for an unset setting, prefers the reply over COLORFGBG, and saves only high-confidence detection. `packages/coding-agent/src/modes/interactive/interactive-mode.ts:954` awaits this before extension startup. | Fixed. PiG previously consulted only COLORFGBG. A light terminal with no hint selected dark, and a conflicting hint overrode the real background. The fix shares one input decoder between startup detection and the main loop. Fixed settings send no query. |
| COLORFGBG parsing | `packages/coding-agent/src/modes/interactive/theme/theme.ts:639-647` uses decimal `parseInt` on each part, scanning right to left. | Fixed. `0;15suffix` and `0;+15.5` now select light instead of falling back to index 0. `15;0suffix` now selects dark instead of index 15. |
| OSC payload parsing | `packages/tui/src/terminal-colors.ts:56-64` takes the first three slash-separated channels. | Fixed. PiG rejected replies with extra channels, including RGBA-shaped replies. |
| Late terminal replies | `packages/tui/src/tui.ts:1407-1431` keeps the pending reply count after timeout; terminal reply consumption precedes listeners. | The new main-mode query retains its pending reply state. Late replies cannot retheme the settled UI or reach editor/modal input and extension listeners. |

The reporter did not identify a terminal or OS. These are demonstrated PiG differences, not a claim that a particular configuration caused that report.

## Regression evidence

- `TestColorDetectionMatchesPi` invokes Pi's installed JavaScript under Node for each environment and platform. It compares capabilities, every authored dark/light theme token's ANSI prefix, COLORFGBG results and OSC parsing. Before the parser fixes, it failed on four COLORFGBG cases and two OSC cases.
- `TestInteractiveStockThemeDetection` drives the main-mode detection path with white, black, RGBA, invalid, silent, explicit-theme and automatic-pair responses. It checks query bytes, precedence, input preservation, settings persistence and late-reply handling.
- `TestInteractiveThemeDetectionCancellationAndErrors` checks cancellation and terminal read errors. It verifies that query-write failure falls back to the environment (`theme.ts:687-706`) and that a settings-save error remains available through `DrainErrors` without ending startup (`settings-manager.ts:613-625,709-711`). The recoverable-error assertions failed first on the initial implementation and passed after matching these upstream handlers.
- `TestInteractiveLateBackgroundReplyPrecedesModalInput` checks that a late reply reaches neither an extension shortcut nor the modal's input handler.
- `interactive-rendering/30-stock-light-256color` failed against real Pi before the startup fix. The retained escaped crop differed at the resumed thinking foreground: PiG's dark-theme index 244 versus Pi's light-theme index 242.
- Scenarios 30–34 cover light/dark on `TERM=screen-256color`, light/dark on `TERM=ansi`, and light on `TERM=tmux-256color` with `COLORTERM=truecolor`. Each sets tmux's actual background before launching the binary and supplies a conflicting COLORFGBG hint. Each compares the full escaped conversation crop, including user backgrounds and assistant/thinking foregrounds, for three pairs.
- Scenario 35 covers the decimal-prefix fallback when tmux has no background to report.
- A compiling mutation that treats the main-mode setting as fixed dark disables the query. It fails the main-mode unit tests and every one of scenarios 30–34. A compiling mutation restoring strict COLORFGBG conversion fails scenario 35. Disabling modal reply consumption fails the modal-input test at the extension shortcut.

The OSC scenarios run in the exclusive scheduler group because Pi's fixed 100 ms terminal deadline is part of the oracle. Concurrent terminal scenarios caused either runtime to fall back before processing a reply. No production timeout, retry count, comparator or capture crop changed. The driver targets `$TMUX_PANE` explicitly when applying `window_style`, so it cannot change another session's background.

## Verification

Passed:

- `go test ./...` with the harness's ambient `PIG_CODING_AGENT_DIR` removed.
- `go test ./tui ./internal/codingagent ./test/parity/...`.
- `go test -race ./tui ./internal/codingagent`.
- `go vet ./...`.
- Windows vet for `./tui`, `./internal/codingagent`, `./test/parity/cmd/lint` and the parity runner's production source files.
- `go tool golangci-lint config verify` and `go tool golangci-lint run` for the touched packages, including the runner with `--build-tags=parity`.
- `make lint-changed LINT_BASE=public/fix/extension-parity-022`.
- `make ci-contracts ci-drift`, after regenerating `test/parity/interfaces/pig-go.json` and coverage.
- The complete `interactive-rendering`, `settings` and `startup` parity families, at their declared durability.

The first whole-repository test invocation exceeded the tool's five-minute command budget. The completed invocation used the same tests and their unchanged deadlines.

The startup family initially inherited the harness's real `PIG_CODING_AGENT_DIR`, which suppressed its expected banner. Removing that ambient variable restored the runner's isolated default home. No scenario assertion changed.

The Windows build of the parity runner's **tests** is blocked by the existing Unix-only `syscall.Kill` call in `test/parity/runner/main_test.go:42`. Its production files pass Windows vet. The default `make lint-changed` cannot find a merge base with `main` in this lane; the task's actual base is `public/fix/extension-parity-022`.

## Resource and performance disposition

`BenchmarkInteractiveThemeDetection` measures a successful immediate OSC reply on the main-mode path. One Linux/amd64 sample reports 1,338 ns/op, 417 B/op and 8 allocs/op. CPU and allocation profiles were captured. This is a path characterization, not a speedup claim.

Detection owns one stopped-on-return timer and retains one small reply-state object for late input. It moves the existing decoder's start earlier rather than starting another terminal reader. The Run context cancels that input pump on return. Detection does not scan Session history and does not add extension IPC to the render loop.

## Scope accounting

`docs/parity/PORT_MAP.md` previously marked the entire theme controller complete based on a pure setting-resolution test. This change replaces that evidence with the main-mode regression and marks the controller partial. Live automatic color-scheme notifications (`theme-controller.ts:159-173`) are still unported; they are not the stock unset-setting path, which Pi persists to a fixed theme after high-confidence detection. This slice does not claim to implement that separate opt-in workflow.

No divergence or lint suppression is added. New runner configuration is test infrastructure, not a Stock PiG feature.
