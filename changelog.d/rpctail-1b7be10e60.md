### Fixed

- `AgentSessionRuntime.dispose` in the Go, Python, Rust and Lisp extension SDKs now settles a run still in flight (it aborts the current session first) before it emits `session_shutdown`, as Pi's `dispose` does; the run's `agent_end` reaches the extension before the shutdown. <!-- fix: 1b7be10e60 -->
- `pig --help` in a build that strips a built-in no longer shows the `pig --tools +codemode` example when `codemode` is stripped. <!-- fix: 1b7be10e60 -->
- A build with `pig_strip_codemode` no longer links the codemode engine through tool search. <!-- fix: 1508b68115 -->
