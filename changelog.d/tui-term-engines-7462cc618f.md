### Added

- `make test-term-engines` replays the alternate screen's scroll-region frames (D205) and Pi's exact bytes (`PIG_TUI_SCROLL_ROWS=0`) of a deterministic corpus through xterm.js, `alacritty_terminal`, `wezterm-term`, libvterm and pyte, and requires each engine to show the same screen after every frame. CI runs it in the `toolchains` shard (`docs/testing/term-engines.md`).
