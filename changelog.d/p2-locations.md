### Added

- p2's locations plugin: the `AgentLocations` contract (fingerprint equal Pi's), the `location_envs`, `location_dirs` and `location_move` tools with which an agent lists the environments it may work on and their folders and moves itself, the main agent's deferred session move (it moves once its turn has ended and no subagent works, and a refused or failed move is recorded), and the server facet that answers only the owner's turns of a current worker. A conversation working in a sandbox only moves, lists and places subagents inside it. <!-- feat: p2-locations -->

### Fixed

- A p2 session's move no longer hangs the worker when a conversation's agent cwd has to change: the worker's `relocate` handler (and `prepare`) now suspends before it waits for the Durable session, whose commit publication is a section of the host's loop, so the section no longer holds the loop its own commit needs. <!-- fix: p2-locations -->
- A Chord singleton state document's handle reads its value in a section of the host's loop, as its watch does: Go code that reads it from a job's goroutine (p2's environment-restart and sharing-archive jobs) no longer races the object's own commits and its seal when the host closes. <!-- fix: p2-locations -->
