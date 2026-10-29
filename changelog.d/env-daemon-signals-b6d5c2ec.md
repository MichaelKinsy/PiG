### Fixed

- The `pi-env` daemon now kills every command it started when it receives `SIGTERM`, `SIGINT` or `SIGHUP`, then exits with status 128 plus the signal. A client that closes its connection ends the daemon with `SIGTERM`; the daemon used to die on it before killing its commands, which were left running under init. On Windows a terminated daemon cannot clean up: its job object ends each command's own process, but not processes that one started. <!-- fix: b6d5c2ec -->
