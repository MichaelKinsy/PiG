### Changed

- A project that is not trusted no longer loads its `AGENTS.md`, `AGENTS.override.md` or `CLAUDE.md` files, from the working directory or any parent directory. The file in the agent directory and the file in your home directory still load. A project with such a file now needs a trust decision, as a project with `.pig/settings.json` does. Print, JSON and RPC runs write one line to stderr that names the files they skipped, and the interactive trust warning names them. Pi loads these files in every project (divergence D210, awaiting owner sign-off). <!-- fix: 16df082b56 -->
