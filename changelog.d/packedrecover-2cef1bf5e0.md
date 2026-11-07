### Fixed

- A packed Go, Python or Rust extension whose factory calls a host helper (`parseFrontmatter`, `Date.parse`, a `pi.events` call) and then panics, raises or returns no extension no longer hangs its load until the caller cancels it: the runner closes the connection the helper opened, and the extension is reported as the load error it is while its siblings load. <!-- fix: 2cef1bf5e0 -->
- When two extensions of one packed cell fail to load, each load error carries its own cause, not the last one the shared process wrote to its log. <!-- fix: 2cef1bf5e0 -->
- A panic in a Go editor component call, in an MCP credential store's `WithRefreshLock` or in a settings storage's `WithLock`, and a failing `onError` callback of `ctx.compact` in a Lisp extension, no longer end the extension's process, which in a packed cell serves other extensions: the call fails with the error instead. <!-- fix: 2cef1bf5e0 -->
