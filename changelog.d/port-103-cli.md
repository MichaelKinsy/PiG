### Changed

- Codemode `image()` also saves each image to a temp file and names the path in the result, so later turns can copy or move generated images.
- Output files (the full text of truncated tool output, binary MCP resources and codemode images) are readable only by the user.
- `Home` and `End` always move the editor cursor. In fullscreen, the transcript top and bottom moved to `Ctrl+Home` and `Ctrl+End`, which no longer move the editor cursor.

### Fixed

- Interactive sessions no longer report a crash, or ask you to run `/bug`, when the terminal goes away while the session reads from it or enters raw mode, for example after closing the window or resuming a suspended `pig` in a closed terminal.
