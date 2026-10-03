# sprites-restore

Branch `sprites-restore`, merged with `agg-100` at 732f16310 (the Pi 1.0 aggregate).

## Look (owner review)

All images in `sprites-restore/` are captured from the real binary in tmux (`capture-pane -e`, default fullscreen mode) and drawn cell by cell. The exception is Apple Terminal, which runs only on macOS; its image is rendered by the header code itself.

- `header-80.png`, `header-120.png`: the startup header at 80 and 120 columns for every built-in sprite, in `/sprite` order. The head is the sprite's 16-by-14 pig (16 cells by 7 lines) in Pi's logo slot, with the version, hints, `Press` line and onboarding line to its right.
- `fallbacks.png`: a 256-color terminal and a 30-column pane (the bold `PiG.` text mark in Pi's 4-cell slot), Apple Terminal (Pi's Apple layout with the text mark), and the narrowest head (31 columns).
- `previews.png`: `/sprite preview <id>` for every sprite: the full `PiG.` wordmark and pig, name and tagline in an overlay.
- `candidates.png`: the head sizes tried. The owner chose A, the 16-by-14 pig at its original size, after seeing the 12-by-8 head (B3) live.

## Items

1. Character sprites pigrogu, darth-vader, kratos, piglet and spider-ham: pixel art byte-identical to the owner's earlier piglogin, pinned by `coding/piglogin/testdata/head-*.golden` and `login-*-120.golden`. No hpe-agentic. pig-default stays the default.
2. `ctx.ui.registerSprite({ id, name, tagline, mascot, palette })` in Go, Rust, Python, Node and the TypeScript declarations. Coverage:
   - the conformance row `sprite-probe` (all transports);
   - bridge tests (validation, UI error, replay, drop);
   - `cmd/pig/extension_sprite_pty_test.go`: a Go, Python and Rust extension's sprite goes through `/sprite list`, `set`, `preview`, a restart with the extension and a restart without it (the header draws pig-default, the saved choice stays).
3. and 15. Logo click (D87). The hit area is the pig's 16 by 7 cells, or the text mark's 4 cells.
   - **Root cause of the owner's dead click:** this branch still defaulted to regular mode, which reports no mouse events. Pi 1.0 and agg-100 default to fullscreen.
   - **Red proof:** `TestFullscreenClickOnTheHeaderHeadPlaysTheAnimation` runs the real binary without `--tui-mode`. It failed before the agg-100 merge ("did not turn on SGR mouse reporting") and passes after it. A hit area 5 cells wider makes it fail on the version click.
   - **tmux confirmation:** a synthesized `\e[<0;8;5M` click on the pig in a fresh tmux run shows the animation's `escape to return` hint.
   - **Unit tests:** `TestHeaderLogoAreaIsTheDrawnPig` checks for every sprite that the hit box is exactly the cells the pig is drawn in (one column or row less fails it); `TestEveryCellOfTheHeaderPigPlaysTheAnimation` clicks every head cell; `TestClickingTheTextMarkPlaysTheAnimation` covers the fallback.
   - **Commits:** 6a09bc544 (red test), 732f16310 (merge with the fix), 315e70437 (hit-box test and summary).
   - **Animation:** it now flies the 16-by-14 pig. The side view it gallops in is three layers thick, so no two of the up to 176 blocks share a position. When two did, Go and Pi's ray caster drew different blocks.
4. Order: pig-default, the eight colors, pigrogu, darth-vader, kratos, piglet, spider-ham, sheriff; all are visible.
7–13. Header and fallbacks:
   - The header shows only the pig, beside the hints, at one size for every sprite.
   - Fallback: the bold `PiG.` text mark in Pi's 4-cell slot, used below 31 columns, without truecolor, and (Pi's wordmark branch) in Apple Terminal.
   - The big art appears only in the `/sprite preview` overlay.
   - D2 now reads "PiG's pig head in Pi's logo slot, 7 rows instead of 2".
14. First-time setup did not run because PiG has no first-time setup. `first-time-setup.ts` was designed out by owner decision on 2026-09-29 (`docs/parity/PORT_MAP.md`, `cmd/pig/first_time_setup_upstream_test.go`). Even Pi 1.0 runs it only when all of these hold (`cli/startup-ui.ts:124-148`):
   - the binary is Pi's own distribution (package `@earendil-works/pi-coding-agent`, app `pi`, config dir `.pi`);
   - `PI_EXPERIMENTAL=1` is set;
   - `PI_CODING_AGENT_DIR` is not set;
   - `~/.pi/agent/settings.json` does not exist.

   A fresh `PIG_HOME` therefore shows no setup, and Pi shows none without `PI_EXPERIMENTAL=1`. PiG's behavior is the faithful port, as Pi's own fork test expects (`TestForkedDistributionDoesNotBlockOnOfficialFirstTimeSetup`). The quickstart and the bundled onboarding doc now state the condition (793f33b23). Item 6 (a sprite step in that setup) needs the owner to reverse the 2026-09-29 decision. The port would bring `first-time-setup.ts` (theme and analytics steps), a gate for PiG (for example `PIG_EXPERIMENTAL=1` with no `settings.json`), and the sprite step. `/sprite preview` already has the art that step would show.

## Bugs found and fixed

- **Header text styles:** the head layout wrapped each beside-line on its own, so styles did not carry across lines as in Pi's single Text. The header text is now laid out as one Text.
- **PiGrogu's wordmark:** its robe symbol `B` also colored the wordmark's eleventh ramp row. The hero now uses digits and punctuation, and a colliding mascot symbol moves to a free one.
- **Preview hidden in fullscreen:** `/sprite preview` drew in the header slot, which fullscreen mode scrolls out of view. It is now an overlay.
- **Animation geometry:** Go used integer `HeadRows/2` where Pi's oracle used 3.5, so the dissolve centered on the wrong row. Running blocks also tied at shared positions.

## Verification

- **Package and SDK tests:** `go test` of `coding/piglogin`, `coding/extension`, the bridge sprite tests, `internal/codingagent`, `internal/pigdocs`, `extensions/sdk`, `cmd/pig` (sprite PTY, print mode, fullscreen click) and `test/extension-conformance` (`TestConformance_TransportsMatch`). Also `cargo test` in `extensions/sdk-rs`, the Python SDK sprite test, and `tsc` over `extensions/sdk-ts`.
- **Race, vet and lint:** `-race -count=24` with `GOMAXPROCS=4` on `coding/piglogin` and `coding/extension`. `-race -count=8` on the header, logo, sprite and head tests. `GOOS=windows go vet` and `GOOS=darwin go vet` on the touched packages. `golangci-lint` on the touched packages: 0 issues.
- **Parity:**
  - startup family: 18/18 hermetic scenarios pass;
  - interactive-rendering: 46/46;
  - fullscreen: 11/11;
  - the extension-runtime scenarios that show the header: 9/9.

## Environment (not this branch)

`make generate` stops at `interface-proposals`: the interface extractor resolves `@earendil-works/pi-agent-core` at the previous release. Also, `.upstream/current` in this worktree still points to the previous mirror, so the test-inventory and known-gaps steps drift. I regenerated the outputs this branch affects one at a time (normalization inventory, coverage) and left the unrelated drift unstaged.
