### Fixed

- p2: the status worker's push and the chat's `abortCompaction` read the followed task graph and view in a section of the host's loop, as Pi's synchronous read does, instead of from their own goroutines (a data race with the Durable publication that applies them). `host.InSection` runs work in the loop for a handler that suspended. <!-- fix: p2-status-race -->
- p2: a reload of a plugin whose new build fails to start is tested to keep the old revision (the failed build is tried again by the next reload). <!-- fix: p2-reload-rollback -->
