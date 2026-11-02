### Added

- `pig --mode rpc --listen <path>` serves one Pi RPC session per connection on a Unix socket, in one process; sessions that load the same extensions share the extension processes, so an additional session costs about 3 MB instead of about 28 MB, and about 12 MB instead of about 107 MB with a Go and a Node extension loaded (D201). Each session keeps its own extension instance, registration, replicated state and events, but module-level state (Node module variables, `process.env`, `globalThis`, timers; Go, Rust and Python package variables) is shared between the sessions in one extension process.

### Fixed

- `events.off` on an extension listener that had migrated to the routed event bus panicked on a nil untrack. <!-- fix: 0e410cdcdf365e9957e6862a154a69bf0c0db7f0 -->
