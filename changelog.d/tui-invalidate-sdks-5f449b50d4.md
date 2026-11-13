### Added

- A `ctx.ui.custom` component is told when the host's screen is invalidated (a theme change, a cell-size report, a change of image support) in every SDK, as Pi calls a component's `invalidate()`: Go `Invalidate()`, Python `invalidate()`, Rust `RemoteComponent::invalidate` / `ViewComponent::invalidate`, Lisp the `:invalidate` function of `make-custom-component`, beside Node's `invalidate()`. The method is optional; the component is drawn again either way, and it sees the new theme when the method runs.

### Fixed

- An extension component in the editor's place (`ctx.ui.custom` without `overlay`) was invalidated three times by one theme change where Pi invalidates it twice, and the components of an extension's own `TuiAltScreen` in the Go, Rust, Python and Lisp SDKs twice per `invalidate()` where Pi does once. Both counts are Pi's now.
- An extension that stopped reading its connection while its component was being invalidated could hold the delivery of that component's keys until the connection ended. Invalidations that cannot be sent are now sent as one message with their count, and the wait ends when the component closes.
