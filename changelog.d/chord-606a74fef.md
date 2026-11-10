### Fixed

- `TestNodeTheCompileCacheIsWrittenBeforeTheProcessExits` no longer counts the probe's own module as a write on the exit path: the host replies to a load before it writes the load's cache entries, so the test now waits for that write with one more request, and its probe loads 300 modules so a count that did not wait fails on every run. The host's comment states what its exit can still write (modules a plugin's call compiled after the last load or reload). <!-- fix: 606a74fef -->
