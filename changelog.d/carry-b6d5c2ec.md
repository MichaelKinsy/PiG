### Fixed

- The `env` daemon kills the commands it started when it receives `SIGTERM`, `SIGINT` or `SIGHUP`, then exits with status 128 plus the signal. `Connection.Close()` sends `SIGTERM`, which used to end the daemon before its kill-all ran and left running commands, and the processes they started, behind as orphans. On Windows nothing changes: the daemon cannot catch `TerminateProcess`. Carried forward from Pi b6d5c2ec, past the 1.4.0+b3a29f11 pin. <!-- fix: b6d5c2ec -->
