### Fixed

- Deliver events and turn boundaries through an invalidated extension runner, as Pi's `emit` and `emitBoundary` do. Only the ctx getters and actions a handler uses report the stale runner.
