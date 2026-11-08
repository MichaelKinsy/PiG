### Added

- p2's agent tools run on the Go host: the coding tools (`read`, `bash`, `edit`, `write` and the rest, in the session's location), code mode with the `agent` one-shot tool, PowerShell, the dev tools, and the `subagent` and `team` tools with their `Subagents` and `SubagentControl` contracts, as Pi's p2 plugins. <!-- feat: p2-agent-tools -->

### Fixed

- A message sent to a conversation while its run is between tool calls no longer waits on the host's loop for a read of the subagent registry, and archiving a subagent from the sidebar no longer blocks the worker. <!-- fix: p2-agent-tools -->
- A p2 reference that ends between its watch's making and its start no longer panics `Watch is stopped` on the follower's goroutine (which took the process down), and the terminal host's attachment is ready only after its frame handler took the host's hello (a call right after could fail with `The terminal host runs older code`). <!-- fix: p2-agent-tools -->
- Redacting a log line no longer sorts and URL-encodes every known secret again each time (they are ordered once, until the set changes), and a test fixture stops the redactor reading the credential files of its removed data directories (each line stat'ed the nine files of every directory ever registered, so a late test of a long-running test binary spent seconds on the stacks of one worker). <!-- fix: p2-agent-tools -->
