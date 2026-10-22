### Added

- Extensions reach Pi's `tui` handle without a component factory: Go `ctx.TUI()`, Python `ctx.tui` and Rust `ctx.tui()` offer `invalidate` and `requestRender`, and the Node runtime's `tui` gains `invalidate` and a `requestRender` that reaches the host's TUI. A display-only Markdown transform can ask for its output to be drawn again after the state it reads changed.
- An extension claims the scheme of OSC 8 hyperlinks with `ctx.OnLinkClick` (Go; Python and Rust `on_link_click`, Node `ctx.ui.onLinkClick`): a click in the fullscreen TUI on a link of that scheme goes to the extension and the host opens nothing (D193). `http`, `https`, `file` and `mailto` stay with the host.
