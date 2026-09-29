### Fixed

- On Windows, resolve a path rooted without a drive (`\x` or `/x`) on the process working directory's drive and keep a trailing dot in a path segment, as Pi's `resolvePath` and Node's `path.resolve` do, in file tools, `--session`, resource, trust, and session paths.
- Fail a path resolution that needs a working directory the process cannot read (for example a removed one) with the operating system's error, as Node's `path.resolve` throws from `process.cwd()`, instead of continuing with a relative path.
- Expand `~` and `file://` in the session file, fork source, trust store and context-file paths, and resolve package install, resource-source and `.agents/skills` paths, with Pi's `resolvePath` and `path.resolve`.
- Resolve the Windows self-update quarantine paths with `toNamespacedPath`, as Pi does.
