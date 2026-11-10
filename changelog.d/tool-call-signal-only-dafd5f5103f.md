### Fixed

- Aborting a run while an extension tool awaits a host call, such as a dialog, a notification, a session read or `exec`, no longer fails that call in the Go, Node, Python, Rust and Lisp extension SDKs. The abort aborts the tool's signal and nothing else, as in Pi: the pending call and later calls return their results, and only the tool's nested `executeTool` calls take the abort as their signal. A tool that has not settled when the 3 s grace period ends is abandoned as before, and what it still awaits is cancelled then. <!-- fix: dafd5f5103f, 0821b337c27 -->
