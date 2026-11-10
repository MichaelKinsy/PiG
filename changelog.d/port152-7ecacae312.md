### Fixed

- When a session's working directory no longer exists, interactive mode now offers Continue, Fork or Cancel (Pi 1.5.2): Continue runs the session in the current directory without storing the choice, Fork copies it into the current directory as a new session so the next resume no longer asks. This applies at startup (`--session`, `--resume`), to `/resume`, to `ctx.switchSession()` from extensions and to `/import`; a failed `/import` removes the copy it made, and Anthropic OAuth requests report Claude Code 2.1.322. <!-- fix: 7ecacae312 -->
