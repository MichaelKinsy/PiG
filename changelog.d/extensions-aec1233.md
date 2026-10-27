### Fixed

- Node extensions read live `ctx.isIdle()` state, so background callbacks detect when a run finishes or starts. <!-- fix: aec1233 -->
