### Changed

- Break the 0.3.0 Go, Rust and Python extension SDKs to match Pi. Host-backed getters return an error (Go `error`, Rust `io::Result`, Python `HostCallError`) instead of an empty value or an assumed default, and Pi's `undefined` is a nil pointer, `Option::None` or `None`. This covers 20 Go `Context` getters, including `*string` for `GetSessionName`, `GetSessionFile` and `GetLeafID`, and `GetBranch` and `GetEntries`, which now return a failed session-log subscription. Go and Rust exec and dialog timeouts become floats (`float64`, `Option<f64>`) (the release already carries the `constrainedSampling: false` union). See the migration tables in the extension documentation.
- Send Pi's collection-complete `systemPromptOptions` to extensions, keep an explicit `constrainedSampling: false` in transcript tool declarations, and echo and arm RPC dialog timeouts as JavaScript numbers.

### Fixed

- Bind the Session's thinking level, context usage and base prompt options into subprocess extensions in print, JSON and RPC modes instead of answering unbound defaults.
