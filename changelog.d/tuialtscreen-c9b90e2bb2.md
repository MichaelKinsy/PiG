### Added

- The Go, Rust and Python SDKs export `TuiAltScreen`, pi-tui's alternate-screen TUI host, with all 45 members (Go `sdk.NewTuiAltScreen`, Rust `TuiAltScreen::new`, Python `TuiAltScreen`). The extension supplies the `Terminal` and the components; the host runs Pi's scrolling viewport, selection, overlays, focus and rendering and calls back for terminal output, component render and input, input listeners and the option callbacks. The SDKs reported the export as divergence D204; it is closed. <!-- feat: c9b90e2bb2 -->
