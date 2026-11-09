### Fixed

- Print and JSON mode: on SIGTERM or SIGHUP the interrupted run now settles before `session_shutdown` handlers run, as the pinned Pi's `runtimeHost.dispose()` does. The session file gets the killed tool's result and the aborted assistant message, no second model request is made, and text mode reports the aborted result on stderr after a throwing handler's error. <!-- fix: 01f1936690eb518d7c036f905065336a018242ef -->
- RPC: a `prompt` that starts a run, and a `prompt` that fails in preparation, answer one microtask later than before, as the pinned Pi's extracted `_preparePrompt` orders them, so `steer` and `follow_up` sent in the same input batch before the prompt are answered first. <!-- fix: df43f7bb53d889ef55c6214e12e5ef6c7e964f08 -->
