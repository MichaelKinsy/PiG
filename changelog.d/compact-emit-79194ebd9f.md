### Fixed

- Aborting a compaction while extensions were being told it finished no longer keeps the extensions after the first from hearing about it. `session_compact` now reaches every extension, in order, as in Pi, whichever of them calls `abortCompaction()` or has the compaction aborted while its handler runs; the saved compaction still completes. <!-- fix: 79194ebd9f -->
