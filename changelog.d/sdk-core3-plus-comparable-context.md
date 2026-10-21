### Added

- Every extension SDK (Go, Rust, Python and Node) gains `OnThemeChange` and `OnToolsExpandedChange` (a change of the host's replicated theme or tool-expansion state, pushed when it changes), `SetWidgetRenderer` (Go, Rust and Python: a widget laid out at the host's width; Node runs Pi's own factory form), `Bell` (`ui.bell`: BEL between frames in the interactive UI, nothing in print, JSON and RPC modes), `ToolCallIndex`, `ToolInput` and `ToolParams` (the tool call's position in its assistant message and its arguments in `Object.keys` order), `ExtensionSourceInfo` (the loader's provenance for the extension) and `OnLoaded` (handlers that run once the extension is registered and before it serves a request). A `pi.events.emit` made while a Go, Rust or Python factory runs is queued and delivered right after the extension is ready, before the `OnLoaded` handlers; Pi delivers it during the factory, and the parity matrix records the difference.

### Fixed

- `Context` and the types that hold one (`EventBus`, `ModelRegistry`, `ReplacedSessionContext`, `SessionManager`, `SetupSessionManager`) in the Go extension SDK are comparable again: the tool-call input a `Context` carries (`ToolInput`, `ToolParams`) is a pointer to the request's arguments instead of a slice, so adding it is no longer an incompatible change.
