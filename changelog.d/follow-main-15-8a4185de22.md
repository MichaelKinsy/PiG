### Fixed

- `enabledModels`/`--models` warnings such as `No models match pattern "github-copilot/nope"` are reported with the other startup diagnostics: in the chat in interactive mode, where they were a raw stderr line printed before the TUI started, and on stderr in print, JSON and RPC modes, after the settings and extension diagnostics. A pattern given twice is reported once (Pi #11973). <!-- fix: 8a4185de22 -->

### Removed

- `coding.ResolveModelScope` from the Go SDK, which printed the scope warnings to stderr itself. Use `coding.ResolveModelScopeWithDiagnostics` and report its `Diagnostics`.
