### Fixed

- Aborting a compaction or a branch summary while an extension's `session_before_compact` or `session_before_tree` handler awaits a host call, such as a dialog, a notification or a session read, no longer fails that call in the Go, Node, Python, Rust and Lisp extension SDKs. The abort aborts the handler's `event.signal` and nothing else, as in Pi: the pending call and later calls return their results, and the handler's reply is kept. <!-- fix: 9a9b153d6bf, 4139cda4739 -->
