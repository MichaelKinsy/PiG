### Added

- `BashToolDetails` (`coding/extension`), `BashResult` (`coding`, `coding/cli`, `coding/rpcclient`), `BashExecutionMessage`, and the `bash`/`powershell` structured output schema (`full_output_error`) now expose `FullOutputError` when truncated output could not be saved to a temp file.
- `tui.AuthSelectorStatusUnresolved()` represents a stored credential whose value did not resolve in `/login`.

### Fixed

- Sessions with very many entries (200k+) after the last compaction are verified to build context entries without stack overflow. <!-- fix: 157db55c3f --> <!-- cause: upstream spread push(...path.slice(...)) overflowed JS call stack; regression test ported -->
- Stored `auth.json` credentials whose `!command` fails now report `AuthStatus{Configured: false, Source: AuthSourceStored}`, render as `stored key did not resolve` in `/login`, and clear their cached command result in `AuthStorage.Modify` when saved again. <!-- fix: fd64f62259 --> <!-- cause: ModelRuntime.GetProviderAuthStatus returned Configured: true for any stored provider and AuthStorage.Modify did not evict the command cache -->
- Windows bash discovery in `internal/shellconfig` and `durable/env/node` now queries `git var GIT_SHELL_PATH` (Git 2.47+) for Git for Windows installed outside Program Files before falling back to `bash.exe` on `PATH` (which usually finds the WSL launcher in System32). <!-- fix: cb3d1e7d3c --> <!-- cause: Windows shell discovery only checked ProgramFiles before falling back to where bash.exe -->
- Self-updating a Windows managed installation (`PI_MANAGED_INSTALL_ROOT` with a Windows entrypoint path in `managed-install.json`) when run from WSL is now refused before any network fetch or package-manager rebuild. <!-- fix: 88f9c1280c --> <!-- cause: managed install check did not reject Windows entrypoints on non-Windows platforms -->
- Temp file write/open/close failures in bash tool output, user `!` bash execution, external editor launch, and TUI width-overflow crash logging no longer crash or drop output; unsaved bash output keeps the retained tail, reports `FullOutputError` / `full_output_error`, strips the model footer in the bash renderer, and avoids false omission markers or non-truncated error reports. <!-- fix: 53d35397b6 --> <!-- cause: unhandled output file errors and missing FullOutputError fallback paths -->
