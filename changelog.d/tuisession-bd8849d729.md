### Fixed

- Quitting during a tool call now waits for the interrupted run to settle before `session_shutdown` and disposal, so the aborted tool result and the final assistant message are saved and extensions see `session_shutdown` after them (Pi #9124, D247). <!-- fix: bd8849d729 -->
