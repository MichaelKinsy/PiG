### Added

- `ctx.ui.custom()` takes a `blocked: { kind, message }` option for an inline component, so terminals that support OSC 7501 program status show PiG as blocked while the component is visible. `kind` is `"question"`, `"permission"` or `"auth"`; overlays ignore the option. Go SDK: `RemoteOverlayOptions.Blocked`; Rust and Python pass `blocked` in their options object; in-process Go: `extension.CustomOptions.Blocked`. <!-- feat: 1d7922152f -->
