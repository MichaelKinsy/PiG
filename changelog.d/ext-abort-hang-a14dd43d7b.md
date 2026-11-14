### Fixed

- An extension that calls `ctx.abort()` from an event handler in interactive mode no longer stops the session from answering. The abort action waited for the turn to end, and the turn waited for the handler that made the call, so the call never returned: the turn stayed open and no later prompt was answered. The call now starts the abort and returns, as in Pi, for extensions of the Node, Go, Python, Rust and Lisp SDKs; an extension that needs the turn to have ended awaits `ctx.waitForIdle()`. Present in 0.2.0 through 0.7.0. <!-- fix: a14dd43d7b -->
