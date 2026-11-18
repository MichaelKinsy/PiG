### Fixed

- An extension handler that called `waitForIdle` outside a command no longer hangs its run. A `tool_call` or `agent_end` handler that waited for the run waited for the run that waited for the handler, in every SDK; as in Pi, only a command context (and a `withSession` callback's context) has `waitForIdle`, so in Node `ctx.waitForIdle` is absent from an event, tool or shortcut context and the call throws `TypeError: ctx.waitForIdle is not a function`, and the Go, Python, Rust and Lisp SDKs get the `not_a_command_context` error back at once (divergence D617). <!-- fix: 2f12a5a0de57ae9f634b2b0e5d4efcd8db00f5c2 --> <!-- cause: D617 left waitForIdle off the command-only list on the assumption that a wait changes no session, overlooking that a handler inside the run waits for itself and that Pi gives the call only to the command context -->

### Upgrading

- One call that worked before is refused now: an event handler that called `waitForIdle` while the session was idle (for example in a `session_start` handler) used to return at once. Call it from a command handler instead, or react to the end of the run in an `agent_settled` handler; the refused call raises (Node: `TypeError`; Go, Python, Rust, Lisp: an error with code `not_a_command_context`).
