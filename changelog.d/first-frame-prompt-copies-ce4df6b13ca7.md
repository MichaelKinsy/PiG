### Fixed

- An interactive start in a project whose AGENTS.md or CLAUDE.md is large no longer keeps three copies of the system prompt in memory. The CLI, the Session and the interactive mode each rendered the same prompt text and each kept it; the Session and the interactive mode now keep the CLI's string when their rendering is equal, so the live heap at the first frame is 130 KB smaller in this repository (2,155,000 B before, 2,025,000 B after, `process.interactive_first_frame.heap_live_bytes`). The prompt text is unchanged. <!-- fix: ce4df6b13ca7 -->
