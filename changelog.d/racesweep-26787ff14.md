### Fixed

- `pig --mode rpc --listen`: a connection that registered just after the server received SIGTERM was shut down while its session was still starting, and the shutdown read the extension runner while the start filled it, a data race. The shutdown now waits until the connection's first session start has bound the extensions (not for its `session_start` handlers). <!-- fix: 26787ff149eabe921bd663ef0bb9503bd78ac69d -->
