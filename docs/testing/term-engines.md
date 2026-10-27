# Terminal-engine A/B test (D205)

D205 lets the alternate-screen renderer scroll the rows that moved with a scroll region (`CSI top;bottom r`, line feeds at the region's bottom, reverse index at its top) instead of drawing every changed row. A frame then writes other bytes than Pi's. `PIG_TUI_SCROLL_ROWS=0` turns it off and writes Pi's exact bytes. The test replays both through real terminal parsers and requires the same screen after every frame.

`make test-term-engines` runs it. CI runs it in the `toolchains` shard (`ci-term-engines`).

## What runs

1. `tui.TestAltScreenTermEngineCorpus` (always run by `go test ./tui`) plays deterministic scenarios twice, as variant **A** (scroll regions, the default) and variant **B** (`PIG_TUI_SCROLL_ROWS=0`), and records each frame's bytes as a step. Scenarios: a streamed transcript that follows its end, streamed tokens with a changing dock, wheel and PageUp/PageDown scrolling (with a fake wheel clock), resizes mid-stream, wide (CJK, emoji, ZWJ, combining) and styled text (truecolor, 256-color, hyperlinks) at an odd width, overlays opening and closing during scrolling, repeated identical rows, seeded random edit mixes, and the pinned Pi fixture's scenarios (`testdata/alt-screen-oracle.json.gz`, 55 scrolled steps among them). The test fails if the corpus does not exercise scrolling (B sets no region, A scrolls in many frames). Nothing is committed: with `PIG_TERM_ENGINE_CORPUS_DIR` set it also writes `plan.txt` and the byte files there.
2. Each engine's runner (`test/term-engines/<engine>/`) reads `plan.txt` and prints, as JSON lines, the screen after every step: row text, style runs (`[column, width, style]`), cursor and scroll margins.
3. `test/term-engines` (build tag `termengines`) compares A with B per engine after every step. There is no golden screen: A is judged against B on the same engine, so an engine's own quirks cancel. A step that differs is reported with the differing rows and the step's bytes in A and B, which is the minimal sequence the engine disagrees with itself on.

The scroll margins (`top`, `bottom`, origin mode, and whether the region is the whole screen) are compared after every step as well, so a frame that leaves a scroll region set is caught even though no row shows it. pyte, libvterm (its DECRQSS and DECRQM status reports), wezterm-term (`get_top_and_bottom_margins`) and xterm.js (`_core._bufferService.buffer.scrollTop/scrollBottom`, internal, so the runner throws if they move) report the region. `alacritty_terminal` keeps it private, so its runner probes a second terminal that replays the same bytes (a line feed on the last row scrolls only if the region ends there, a reverse index on the first row only if it starts there) and reports whether the region is the whole screen. Pi's bytes (B) must leave the margins as the whole screen with origin mode off.

The cursor's position is compared only while it is shown (a hidden cursor's position cannot be seen, and every frame addresses its rows absolutely); whether it is shown is compared always.

## Formats

`plan.txt` is tab-separated: `scenario NAME COLS ROWS`, then for variants `A` and `B` a `variant A|B FILE` line followed by one `step END [COLS ROWS]` line per frame. `END` is the byte offset in FILE at which the frame's bytes end; `COLS ROWS` resizes the terminal before the frame's bytes. A runner starts every variant at the scenario's size and prints a first line `{"engine": ...}`, then one line `{"scenario", "variant", "checkpoints": [{"cursor": [x, y], "rows": [...], "styles": [[[column, width, style]...]...]}]}` per variant, one checkpoint per step. A style is the engine's own words for the cell's non-default colors and attributes.

## Engines

| Engine | Used by | Pin | Runner |
|---|---|---|---|
| xterm.js (`@xterm/headless`) | VS Code, Cursor, Hyper, Tabby, JetBrains | `xterm/package-lock.json` | Node |
| `alacritty_terminal` | Alacritty, Zed | `alacritty/Cargo.lock` | Rust |
| `wezterm-term` | WezTerm | `wezterm/Cargo.lock` (git commit; the crate is not on crates.io) | Rust |
| libvterm | Neovim, Vim | `automation/dev/toolchains.lock` (release tarball, sha256) | C, built by `automation/ci/test-term-engines.sh` |
| pyte | strict reference | `pyte/requirements.txt` (hashes) | Python |

`automation/ci/test-term-engines.sh` installs each from its lock (`npm ci`, `cargo build --locked`, `pip --require-hashes`, a digest-checked tarball) into `$PIG_CACHE_HOME/pig-term-engines`, keyed by the lock, so a lock change rebuilds. A missing toolchain or a digest mismatch fails; nothing skips. Dependabot covers the four manifests. Moving a pin: bump the manifest, regenerate its lock, run the target.

libvterm 0.3.3 has no dim attribute, and pyte has no alternate screen (it ignores `?1049h`), so each shows what its model allows; both still see the same bytes in A and B.

## Known findings

A scenario named `known-*` (`termKnownScenarios` and the `known-unclipped-*` copies) is a recorded divergence. It must differ in every engine and fails once it stops, so a fix deletes it.

- `known-clipped-sgr-leak`: a component that renders a row wider than the screen. Pi clips the row after adding its closing SGR reset (`applyLineResets`, then `sliceByColumn` in `tui-alt-screen.ts` `doRender`), so the reset is cut off with the clipped text and the row's attributes stay on in the terminal; Pi's recorded bytes show it. The attributes color the rows written after the clipped row, so which rows show them depends on which rows each variant writes (B writes every changed row, A moves the others). Rows that fit the screen never leak.
- `known-unclipped-stream-follow`, `-resize-mid-stream`, `-wide-styled` and `-random-mix-2`: the scenarios that first showed other screens in A than in B, played with their components rendering over-wide lines as they are. Each differs in all five engines; the scenarios of the same names, whose components clip to the screen and close the clip with a reset, are equal. That is how each is shown to differ only by the leak.
- Fixture scenarios 173 and 485 (`termLeakingOracleScenarios`) render over-wide lines and show other screens in some engines as they are (`leak-oracle-173`, `leak-oracle-485`, reported, and required to differ in at least one engine, else they come off the list). Their twins `oracle-173-closed` and `oracle-485-closed`, the same scenarios with every over-wide rendered line clipped and closed identically in A and B, are in the equality and equal in all five engines. The other fixture scenarios that render an over-wide line show the same screens as they are and stay in the equality.

## Not covered

Image rows (their escape sequences place images, which no grid shows; D205 never scrolls them), Ghostty, Terminal.app, iTerm2, Windows conhost and Windows Terminal (checked by hand, and A and B are byte-identical in conhost), tmux, Zellij and GNU screen.
