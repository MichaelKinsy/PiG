### Added

- `pig --mode rpc --listen <path>` serves one Pi RPC session per connection on a Unix socket, in one process, so an additional session costs a few MB instead of a process of its own. Every session has its own extension processes, so no extension process, and none of its module-level state, is shared between sessions (extensions compiled into the host program run in the server process in both modes). `--listen-share-extensions` makes the sessions share the extension processes, which cuts the cost of a session that loads extensions further; module-level state (Node module variables, `process.env`, `globalThis`, timers; Go, Rust, Python and Lisp package variables) is then shared between the sessions that share a process, and the flag's help line says so (D232). Contributed by @balcsida (#404).

### Fixed

- `events.off` on an extension listener that had migrated to the routed event bus panicked on a nil untrack. <!-- fix: ada701819cf5aa940ce24da7b6d759ce6af52520 -->
