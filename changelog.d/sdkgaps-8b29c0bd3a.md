### Added

- The Go, Rust and Python SDKs serve Pi's `new ModelRegistry(runtime)`: a registry facade over a `ModelRuntime` the extension built, whose members all forward to that runtime, and the runtime's `registerNativeProvider`, `getRegisteredNativeProvider`, `getProvider` and `getProviders`. A Provider object an extension registers on its own runtime stays in the extension and the runtime calls it over the connection; a builtin provider's object still fails with the D78 error, while its display name is answered.
