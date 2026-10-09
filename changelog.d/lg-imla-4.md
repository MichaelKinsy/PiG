### Fixed

- `createPowerShellTool` now uses the `operations` option. The PowerShell tool dropped it and always ran commands through the local PowerShell.
- The editor's autocomplete list takes its colors from one `getSelectListTheme` equivalent, the same function the other select lists use.

### Added

- `SessionManager.setSessionFile` switches an existing session manager to another session file, as Pi does for resume and branching: it loads a file with entries, initialises an empty file with a header, and starts a new session at a missing path. The manager keeps its working directory, and an in-memory manager never writes the file.
- `DynamicBorder` takes a color function, as Pi's constructor does.
