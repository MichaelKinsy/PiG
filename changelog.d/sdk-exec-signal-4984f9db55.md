### Upgrading

- Rust SDK: `ExecOptions` has a `signal: Option<ProviderSignal>` field, so a struct literal needs it or `..Default::default()`. Before: `ExecOptions { timeout: Some(250.0), cwd: None }`. After: `ExecOptions { timeout: Some(250.0), ..Default::default() }`. `ProviderSignal` now implements `Debug` and `PartialEq` (two signals are equal when they are the same signal).
- Go SDK `ExecOptions` has a `Signal context.Context` field. Code that builds the struct with named fields is unchanged; an unkeyed literal needs the new field.

### Added

- `pi.exec`'s `options.signal` works in every extension SDK, as in Pi: Go `ExecOptions.Signal`, Rust `ExecOptions.signal`, Python `Context.exec(..., signal=)` and Node `options.signal`. When the signal aborts, the host sends SIGTERM to the command (it kills the process on Windows), and the call returns the output written so far with `killed` set and code 0, as Pi's does. Like Pi, only the command is signalled, not its descendants, and a command that ignores SIGTERM keeps the call waiting. A signal replaces the calling request's cancellation: cancelling the request neither ends the command nor returns from the call. In-process extensions pass the signal in `extension.ExecOptions.Signal`.

### Fixed

- A command that an extension started with `pi.exec` and an `AbortSignal` kept running after the signal aborted: the signal never reached the host in the Go, Rust and Python SDKs, and the Node runtime sent it as an empty object. In print, JSON and RPC mode the command did not even end with the request, because the host ran it under a context that nothing cancels. <!-- fix: 4984f9db55 -->
