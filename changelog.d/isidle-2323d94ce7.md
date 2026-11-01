### Fixed

- `ctx.isIdle()` read on the statement after a triggering `pi.sendMessage(..., { triggerTurn: true })` is busy as in Pi in every SDK, in print, JSON and RPC mode, isolated and packed: the Host answers busy for every extension from the send until the handler that sent it returns, awaits a Promise-returning member or (Node) awaits, where a run that settled while the read was in flight made the Go, Python, Rust and Lisp SDKs report an idle session. The Node runtime's own hold is gone; a message sent from `agent_settled` still reads the Session's idle answer. <!-- fix: 2323d94ce7 -->
