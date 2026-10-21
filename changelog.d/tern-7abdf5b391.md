### Upgrading

- Go: `ModelRegistry.Find` now returns `(map[string]any, error)`. Receive the error (`model, err := ctx.ModelRegistry().Find(provider, id)`); `nil, nil` still means no model matches, and a failed host call is the error. The SDK upgrade rules name the change (`model-registry-find-returns-error`) but do not rewrite it. <!-- fix: 7abdf5b391 -->
- Rust: `ModelRegistry::find` returns `io::Result<Option<Value>>` instead of `Option<Value>`. Propagate or handle the `Err`; `Ok(None)` means no model matches. <!-- fix: 7abdf5b391 -->
- Python: `ModelRegistry.has_configured_auth` and `is_using_oauth` return `False` for a model with no `provider` instead of raising `KeyError`; drop any `except KeyError` that relied on it. `get_entries()` against a host with no session binding raises instead of returning `None`; do not test its result for `None`. <!-- fix: 7abdf5b391 -->

### Fixed

- Extension SDKs: `ModelRegistry.find` reports a host or transport failure instead of "no such model". The Go `Find` returns `(model, error)` and the Rust `find` returns `io::Result<Option<_>>`; Python already raised. A host with no `getModel` or `sessionRead` binding answers an error, so a list-typed session read is never a null. <!-- fix: 7abdf5b391 -->
- Python SDK: `has_configured_auth` and `is_using_oauth` answer `False` for a model with no provider, as Pi and the Go, Rust and Node SDKs do, instead of raising `KeyError`. <!-- fix: 7abdf5b391 -->
- Go SDK: `Context.CancellationReason` returns the reason the host gave when it cancelled the request, as the Python and Rust SDKs do. <!-- fix: 7abdf5b391 -->
