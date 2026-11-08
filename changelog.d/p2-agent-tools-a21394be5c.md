### Added

- p2's agent tools run on the Go host: the coding tools (`read`, `bash`, `edit`, `write` and the rest, in the session's location), code mode with the `agent` one-shot tool, PowerShell, the dev tools, and the `subagent` and `team` tools with their `Subagents` and `SubagentControl` contracts, as Pi's p2 plugins. <!-- feat: p2-agent-tools -->

### Fixed

- A message sent to a conversation while its run is between tool calls no longer waits on the host's loop for a read of the subagent registry, and archiving a subagent from the sidebar no longer blocks the worker. <!-- fix: a21394be5c4aae501d21c1a87a0ed1645099e580 -->
- A p2 reference that ends between its watch's making and its start no longer panics `Watch is stopped` on the follower's goroutine (which took the process down), and the terminal host's attachment is ready only after its frame handler took the host's hello (a call right after could fail with `The terminal host runs older code`). <!-- fix: a21394be5c4aae501d21c1a87a0ed1645099e580 -->
- A test fixture stops the log redactor reading the credential files of its removed data directories (each line stat'ed the files of every directory ever registered, so a late test of a long-running test binary spent seconds on the stacks of one worker). <!-- fix: 547d5898eb1265c4b2b26215e65d504b28d8b4b4 -->
- Removing code mode while a script runs no longer swallows every panic of telling the script why: a call that settled meanwhile is still the expected end, anything else is reported on stderr. <!-- fix: 547d5898eb1265c4b2b26215e65d504b28d8b4b4 -->
