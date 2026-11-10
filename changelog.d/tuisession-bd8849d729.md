### Fixed

- Quitting through `Runtime.Close` or the interactive quit during a tool call now waits for the interrupted run to settle before `session_shutdown` and disposal, so the aborted tool result and the final assistant message are saved and extensions see `session_shutdown` after them. Print mode's termination-signal shutdown and RPC shutdown keep Pi 1.5.1's order (Pi #9124, D247). <!-- fix: bd8849d729, c28332bfcb, 694685ee71 -->
