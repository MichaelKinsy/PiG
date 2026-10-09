### Changed

- `agent.NewAgent` returns `(*Agent, error)`. It fails with `ErrNoDefaultStreamFunction` when no `StreamFn`, `DefaultStreamFn` or configured default exists, as Pi's `Agent` constructor throws. A model's `Provider` no longer stands in for the stream function.

- The TUI uses Pi's names and input pipeline: `ShowOverlay`, `GetFocusedComponent`, `AddInputListener`, `RemoveInputListener`, `OnTerminalColorSchemeChange`, `SetTerminalColorSchemeNotifications`, `RunInputListeners`, `ConsumeTerminalColorSchemeReport` and an exported `CompositeTuiLine`. `Start` and `Stop` re-enable and disable terminal light/dark reports, so the external editor, suspend and renderer swap no longer write them by hand.

### Fixed

- `StreamProxy` merges the final tool call and reports abort and timeout as Pi does, and it no longer changes a delivered message when the server sends more after the terminal event. The stdin buffer drops an incomplete sequence before a bracketed paste, the editor calls `onChange` as Pi does, and overlays hide the cursor and request a frame as Pi does.

- The `tui` package's default keybindings no longer depend on the platform: undo is `ctrl+-` and the alt-screen prompt and search keys are Pi's `TUI_KEYBINDINGS` everywhere. The Windows and WSL keys stay in the coding agent's table, as in Pi.

### Added

- `PI_TUI_WRITE_LOG` records the terminal write stream as Pi does: a file path is appended to, an existing directory gets `tui-<date>_<time>-<pid>.log`, and cursor, title, progress, start and stop sequences are not logged.

- `agent` exports Pi's standalone loop: `RunAgentLoop`, `RunAgentLoopContinue`, `AgentLoop`, `AgentLoopContinue`, `AgentContext`, `AgentLoopConfig`, `AgentEventSink` and `AgentEventStream`. The `Agent` runs on the same loop. `AgentLoopConfig`, `AgentOptions` and `Agent` take a `GetAPIKey` resolver that runs before every request, as Pi's `getApiKey`; its error fails the run, as a rejected `getApiKey` does. `AgentLoop` logs a failed run on the process log and ends its stream with the error.

- `tui.ParseColor` accepts a number, and `IsKeyRepeat` is exported.
