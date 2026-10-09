### Added

- Added `outputPad` to the tool render context (`context.outputPad` in Node extensions, `OutputPad`/`output_pad` in the Go, Rust and Python SDKs). Renderers with `renderShell: "self"` apply it themselves.
- Added `Box.SetPaddingX(...)` and `Text.SetPaddingX(...)` to the TUI package.

### Changed

- Changed `outputPad` to also apply to `!` command output, tool output, and branch, compaction and skill summary blocks, and to extension custom messages and entries. Changing it in `/settings` now updates the transcript in place instead of rebuilding it.

### Fixed

- Fixed Markdown links not being clickable in Herdr: `TERM_PROGRAM=herdr` is now detected as supporting OSC 8 hyperlinks, with images off.
- Fixed the fullscreen text selection and multi-click state surviving a session switch or other transcript rebuild, which highlighted unrelated text in the new transcript.
- Fixed `!` and RPC `bash` output keeping fragments of color codes, such as a stray `m`, when a code was split across output chunks.
