### Added

- Added program resume through the [Program Resume Protocol (OSC 7577)](https://www.superlogical.com/rex/docs/build/program-resume): in terminals that set `OSC7577_TOKEN`, interactive mode reports `pig --session <id>` (or a Piglet Binary's own name) for the current session so the terminal can bring it back after a restart. PiG removes `OSC7577_TOKEN` from the environment at startup so shells, editors, MCP servers, and extension processes cannot see or abuse it. See [Terminal setup](/docs/latest/terminal-setup#program-resume).

### Fixed

- Fixed a Piglet Binary's session-exit resume hint to print the binary's own name instead of `pig` (`ResumeProgramName`). <!-- fix: 1e01e497e6,c448089b49 --> <!-- cause: exit resume hint hardcoded pig instead of the active binary name -->
