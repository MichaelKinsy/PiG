### Added

- `pig --mode rpc --listen <path>` serves one Pi RPC session per connection on a Unix socket, in one process; sessions that load the same extensions share the extension processes, so an additional session costs about 3 MB instead of about 28 MB, and about 12 MB instead of about 107 MB with a Go and a Node extension loaded (D232). Each session keeps its own extension instance, registration, replicated state and events, but module-level state (Node module variables, `process.env`, `globalThis`, timers; Go, Rust and Python package variables) is shared between the sessions in one extension process (exact standalone extensions and quarantined cells keep their own process).

### Fixed

- `events.off` on an extension listener that had migrated to the routed event bus panicked on a nil untrack. <!-- fix: ada701819cf5aa940ce24da7b6d759ce6af52520 -->
