### Fixed

- A Lisp extension's host call that the host had already answered no longer fails with `host-call-cancelled` when the request is cancelled before the waiting thread wakes, so a `ctx-notify` in a handler cancelled at that moment no longer ends the handler with no result. <!-- fix: 0c4a06b0e4d, f8180c331b0 -->
