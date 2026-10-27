### Added

- The Go, Rust and Python SDKs export Pi's `createAgentSessionServices` and `createAgentSessionFromServices`: the services of one session cwd (model runtime, settings manager, resource loader, diagnostics) and a session built over them, with the new `sessionStartEvent` option on child sessions. The registrations and extension flag values of the extensions the services' loader loaded apply as in Pi, and an extension a session loads from such a loader now reads those flag values with `getFlag`.
