# fix-114-pig-header

Lane: PiG shows the PiG mark, never Pi's logo (owner decision 2026-10-01, ships in 0.4.0). Base: the release-040-content integration branch.

## Design

- `coding/piglogin` (new, core): the ten sprites, the selection state (`$PIG_HOME/state/pig-standard/login.json`, the file the Pigpen games read), the header mark renderer and the built-in `pig-login` extension (`/sprite`). Ported from PiG Standard's `piglogin` (MIT, Michael Kinsy) through Pigpen's `components/pig-login` + `pig-play/libraries/sprite` (the games-mcp-jev branch).
- The host's built-in header (`internal/codingagent/startup_header.go`) draws the PiG mark where Pi draws its logo. Pi's logo is 4 cells by 2 lines, so the mark is 2 lines tall (half blocks) and fits the hint line of the compact header at 100 columns (18 cells). When the sprite plus the first hint line do not fit, the header uses the one-line `PiG.` mark, 4 cells wide like Pi's logo, so wrapping is Pi's.
- `setHeader(undefined)` and `/reload` restore the built-in header, which is the PiG header, so Pi's logo never appears. An extension's `setHeader` replaces it as in Pi.

## Red run

Upstream has no tests for this behavior (owner-approved divergence), so the red tests assert the divergence's contract. Against signature stubs in `coding/piglogin` and the unchanged Pi-logo header, `go test ./coding/piglogin ./coding/extension/builtin ./internal/codingagent` fails 36 tests, each for the right reason (empty catalogue, empty marks, Pi logo in the header, no `pig-login` built-in). `TestQuietStartupDrawsNoHeader` and `TestVerboseHeaderExpansionAndRestoration` pass before the fix on purpose: they guard behavior that must not change.

## Green

- `coding/piglogin`: sprites (the ten, PiG website green default), state, `SpriteMark`/`FallbackMark`/`MarkFor`, `/sprite` extension. `coding/extension/builtin` lists `pig-login` last, replaceable, so PiG Standard's own `piglogin` (or any extension registering `/sprite`) takes the command.
- `internal/codingagent/startup_header.go`: the PiG mark replaces `piLogoLines` (removed with its test; `pi-logo.ts` is `n/a` in PORT_MAP). The sprite is 18 cells by 2 lines and is used only while sprite + space + the first hint line fit `width-2`; otherwise the 4-cell `PiG.` mark, so wrapping equals Pi's at every width (`TestBuiltInHeaderFallsBackToTheOneLineMarkWhenNarrow` derives the expected wrap from Pi's layout). At 100 columns the compact header uses the sprite (98 cells exactly).
- `setBuiltInHeader` (reached by `setHeader(undefined)`, `/reload`, session replacement) re-reads the saved sprite off the render loop.
- Color modes: the mark follows the theme's terminal color mode (truecolor or 256color), as Pi's logo does. Pi's interactive TUI has no NO_COLOR or 16-color mode (the tui package has two modes and `chalk_color.go` records that NO_COLOR is not consulted), so there is nothing further to honor.
- Quiet startup and non-interactive modes: unchanged (`LoginVisible`), `TestQuietStartupDrawsNoHeader`.
- Real-binary check (tmux, temp `PIG_HOME`): header at 100 columns in regular and `--tui-mode fullscreen`; `/sprite set sheriff` + `/reload` redraw the sheriff hat and write `{"variant":"sheriff"}` (mode 0600, directory 0700); `/sprite list` lists ten.

## Tests

- `coding/piglogin`: 21 tests (catalogue, state, ConfigHome, Activate/Refresh, mark pixels against an independent grid in both color modes for all ten sprites, fallback, MarkFor boundaries, `/sprite` list/set/errors/picker/dismissed/save failure).
- `coding/extension/builtin`: 2. `internal/codingagent`: 10 header tests (sprite, fallback at 99/90/86/85/84/80, boundary widths, 256 color, selected sprite, `/reload`, another extension's `setHeader` and restore, quiet startup, narrowest widths, Apple Terminal text mark).
- Parity: `startup` (17, new 13 compares everything from the first hint on at 80 columns, 00 pins the mark's first line and forbids Pi's), `interactive-rendering` (46), `fullscreen` (10), `slash-commands` (16), `footer` (9), `project-trust` (23), `extensions-runtime` (95), `selectors` (14), plus one `make parity-fast` pass in which only the three config-selector scenarios failed (PiG lists `pig-login`); they now remove that one row, and `get_commands` scenarios 26, 28 and 55 remove the one `sprite` entry.
- Load: `-race -count=24`, `GOMAXPROCS=4`, and `taskset -c 0-3` with four CPU burners (PIDs recorded, killed): green.
- Mutations (all killed): fit margin `-1`, `>=` to `>`, drop `Refresh`, `Activate` not selecting, `Replaceable: false`.
- Environmental: `extensions/sdk-ts/node_modules` is symlinked from `agg-992` (the 0.99.2 upstream) in this worktree only (not committed).

## Check against the v1.0.0 upstream mirror (`.upstream/v1.0.0`)

- `pi-logo.ts` and `withLogo` keep the same 2-line logo and layout; PiG's mark sits in the same slot. `interactive-mode.ts:998-1006`: Apple Terminal gets `Pi vX` text and the hints below. PiG already does this (`supportsHalfBlockMark`, `tui.IsAppleTerminalSession`) with the `PiG.` mark.
- `quietStartup: "header"` (interactive-mode.ts:1409-1415): the header stays and the details hide. The PiG mark is drawn whenever `LoginVisible` is true, so the porter only has to set `LoginVisible` from `shouldShowStartupHeader` (hidden only by `true`) and make `compactOnboarding` say "and loaded resources" only when details show; nothing in `coding/piglogin` depends on it.
- Fullscreen default: the header is in the same container in both modes; checked in the real binary under `--tui-mode fullscreen`.
- Click-the-logo animation (`BuiltInHeader.handleMouse`, `pi-logo-animation.ts`): skipped, as instructed. Mouse clicks on the first two lines do nothing in PiG.
- `PiG` product name vs `Pi can explain...` onboarding: unchanged (D2).

## Notes for the lead

- Pigpen's `components/pig-login` is now redundant: stock PiG ships `/sprite` and reads/writes the same `login.json`. Remove it from Pigpen (and its CREDITS/relocation records) separately.
- Stale prose outside this lane: AGENTS.md's product-extension boundary and `docs/site/docs/{derivative-harnesses,piglets,quickstart}.md` still say PiG Standard's `piglogin` owns the identity and `/sprite`. That stays true of Standard's own extension, which replaces the built-in one; the owner decision moves the default into Stock PiG, so the boundary text needs the lead's edit.
- The sprite state root follows the SDK's `ConfigHome` ($PIG_HOME, else ~/.pig), not `codingagent.ConfigRoot()` (which also honors XDG_CONFIG_HOME), so games and PiG always agree.
- Stubs other families must fill: none.
- Deferred: none. No upstream 0.99.2 test covers this behavior (owner-approved divergence), so there is no upstream test file to map.
