### Removed

- `agent.SettleGate`, `agent.SettleSlot` and `agent.SettleGate.SetParkedHook` are removed from `github.com/MichaelKinsy/PiG/agent`. They ordered the continuations of waiters that settle back to back (a parallel tool batch's `tool_execution_end` events, the subprocess host's dialog replies) and moved to the internal package `internal/jsloop` before any release carried them, so there is no public replacement. `agent.ToolSettleFromContext` stays exported and its call pattern is unchanged: it returns the slot of one call of a parallel batch, now of the internal type, so an embedder can call `Settle`, `Run` and `Close` on the result but can no longer name the type.

### Upgrading

- If your code declared an `agent.SettleGate` or held an `*agent.SettleSlot` (for example in a tool that settles its own slot through `agent.ToolSettleFromContext`), stop naming the types: keep the value `ToolSettleFromContext` returns in a local variable and call its methods, or drop the call, because a tool that never settles its slot settles it when `Execute` returns. An extension needs no change: extensions in every SDK keep the same order through the wire, not through this type.
