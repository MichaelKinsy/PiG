### Added

- A Piglet can set `contextFiles: trusted-only`. A run of that Piglet loads no `AGENTS.md`, `AGENTS.override.md` or `CLAUDE.md` of a project that is not trusted, and a project that has such a file needs a trust decision. The file in the agent directory and the file in your home directory still load. Print, JSON and RPC runs write one line to stderr that names the skipped files, and the interactive trust warning names them. Stock pig is unchanged (D211, awaiting owner sign-off).
