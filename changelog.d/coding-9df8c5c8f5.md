### Fixed

- `--session <id>` and `--fork <id>` with an exact session ID now read only the session headers of the project's session directory, as Pi does, and list sessions only to match an ID prefix or to search other projects. Resolving an exact ID no longer decodes every session first: with 20 sessions of 1000 messages it allocated 21.7 MB, growing with the sessions, and it now allocates a size-independent amount. <!-- fix: 9df8c5c8f5 -->

