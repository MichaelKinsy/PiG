### Fixed

- `pi.exec` reports `killed: true` for a timeout that came due while the call waited, as Pi does: a host that kept the process off the CPU past the timer and past the child's end (a command that traps SIGTERM and finishes by itself) settled with the cancellation not yet delivered and reported `killed: false`. <!-- fix: 22ef80f12 -->
