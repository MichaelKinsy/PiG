### Added
- `tui.Key`, the helper for building key identifiers (`Key.Ctrl("c")`, `Key.Escape`), matching Pi's `Key`.

### Fixed
- The login dialog now holds TUI focus while it is shown, so the prompt cursor appears in the right place for IME input.
- `SessionBeforeTreeResultSummary.Usage` is typed as `ai.Usage`.
- Tool cards rebuilt when a session is resumed now follow the `showImages` and `imageWidthCells` settings.
