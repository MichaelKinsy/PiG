### Changed

- Chord's terminal launcher takes the terminal as `TerminalOptions.Input` and `TerminalOptions.Output`, as Pi's `startTerminal` takes `input` and `output` (raw mode, size and resizes found by method on them) instead of one `Terminal` value; the bridge messages' `Kind` fields are named string types with constants; `tui.OptimisticAdd` returns its call's result as Pi's does (`OptimisticAdd[A, R]`); `tui.TerminalTheme` ports Pi's terminal theme lookup. <!-- change: 9c1e5b7a40 -->
