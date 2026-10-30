### Fixed

- A Lisp extension no longer ignores an `events.dispatch` request that the host sends after registration and before `ready`: the request was read and dropped, so the host waited for an answer that never came. The Lisp SDK now answers it once, under its own request ID, with its own payload, as the Go, Rust, Python and Node SDKs do. <!-- fix: fa0eadbe18 -->
