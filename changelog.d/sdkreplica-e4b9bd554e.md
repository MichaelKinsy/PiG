### Changed

- The Go, Rust, Python and Lisp extension SDKs also answer the session name and the session file (`GetSessionName`, `GetSessionFile`, the session manager's `getSessionName` and `getSessionFile`) from the host's state snapshot instead of a host call per read. A snapshot's session replaces both, so a replaced in-memory session clears the file the previous one had. The leaf id stays a host call, because it moves with every committed entry, which no state push announces.

### Fixed

- A Node extension's `ctx.getContextUsage()` returned the value of the host's last dispatch, so a timer between requests, or a handler after `waitForIdle`, `compact` or `navigateTree`, read a stale usage where Pi reads it live. It now asks the host, like the Go, Rust, Python and Lisp SDKs, and the host no longer scans the session branch for the usage on every state push. <!-- fix: e4b9bd554e -->
