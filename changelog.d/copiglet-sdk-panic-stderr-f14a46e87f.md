### Fixed

- Go SDK: the stack of a panic the SDK recovered in a settings storage's `WithLock`, an MCP credential store's `WithRefreshLock` or a custom editor component (a queued task, a guarded call such as `HandleInput`) was written to the extension process's stderr, which the host forwards to the terminal, so it painted over the interactive screen. It now goes to the extension's log, as the stacks of the SDK's other recovered panics do; the user still sees the one-line error (Pi shows one line for a throwing callback and writes no stack to the console). <!-- fix: f7dbf5424d -->
