### Added

- Extension SDKs (Go, Python, Rust): Pi's coding-agent theme and key-hint helpers `getMarkdownTheme`, `getSelectListTheme`, `getSettingsListTheme`, `getEditorTheme`, `highlightCode`, `initTheme`, `keyHint`, `keyText`, `rawKeyHint` and `truncateToVisualLines`, held to the pinned Pi package's own output by vectors (`test/parity/sdk-vectors/pi-coding-agent/`) and by a conformance row per SDK against the real host (Node runs Pi's own functions in the same rows). The theme helpers read a process-global theme (the host's palette, or the one `initTheme` installed until the host's theme changes); `highlightCode` asks the host's highlight.js port over the new pre-registration `highlight.code` call and `initTheme` the host's theme registry over `theme.load`. `Theme.sourceInfo` is carried in the host palette and read by the three SDKs. <!-- fix: 6d00b4afff -->

### Fixed

- Extension SDKs: `formatKeyText`/`keyDisplayText` upper-cased the first rune of a part, where Pi's `charAt(0).toUpperCase()` leaves a character beyond the Basic Multilingual Plane (half a surrogate pair) as it is. <!-- fix: 6d00b4afff -->
