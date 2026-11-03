### Fixed

- Starting a new Session in a working directory that was removed, where the system still reports its path (macOS), ends with Pi's `Stored session working directory does not exist` message and exit 1 in print, JSON and RPC mode, and offers to continue in the current directory in interactive mode, instead of failing later with a `construct session` error. <!-- fix: eba1115c50 -->
