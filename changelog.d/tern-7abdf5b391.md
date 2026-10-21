### Fixed

- Extension SDKs: `ModelRegistry.find` reports a host or transport failure instead of "no such model". The Go `Find` returns `(model, error)` and the Rust `find` returns `io::Result<Option<_>>`; Python already raised. A host with no `getModel` or `sessionRead` binding answers an error, so a list-typed session read is never a null. <!-- fix: 7abdf5b391 -->
- Python SDK: `has_configured_auth` and `is_using_oauth` answer `False` for a model with no provider, as Pi and the Go, Rust and Node SDKs do, instead of raising `KeyError`. <!-- fix: 7abdf5b391 -->
- Go SDK: `Context.CancellationReason` returns the reason the host gave when it cancelled the request, as the Python and Rust SDKs do. <!-- fix: 7abdf5b391 -->
