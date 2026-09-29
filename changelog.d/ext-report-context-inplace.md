### Fixed

- Keep a `context` handler's in-place edits to `event.messages` when a Go handler returns a nil `messages` list, and when a Rust handler returns no result or a null `messages` value, as Pi does.
