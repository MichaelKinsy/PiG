### Added

- `chord.Watchable[T]`, the common interface of values that can be watched: `Value` and `Watch(ctx)`, which returns a `WatchHandle`. Ports Pi's `Watchable`.
- `ResolvedPaths.InstallFailures` (`installFailures` on the extension SDKs' resolve result) lists the configured packages that were skipped because installing them failed.

### Changed

- A pi-messages `done` or `error` event that carries `responseModel` sets it on the assistant message, so a virtual model reports the physical model behind it.

### Fixed

- Claude models with per-message effort no longer fail with `Invalid effort level` on accounts whose organization caps effort below `high`: the fixed top-level effort is `low`. <!-- fix: 930c88ec0c -->
- MCP sign-in now rejects authorization endpoints and other OAuth metadata URLs that are not `http:` or `https:`, so a malicious server cannot make pig launch a `file:` or custom protocol URL. <!-- fix: 619443a024 -->
- A configured package or `-e` source that cannot be installed no longer ends startup. It is skipped, as in offline mode, and reported as an extension warning that says how to remove it (`pig remove <source>`, with `--local` for a project package) or to check the source; `--help` and `--list-models` print it too. <!-- fix: 07da4ea3a6 -->
- A line of many `>` characters no longer makes the Markdown renderer's output grow exponentially with the nesting depth. <!-- fix: ae498ebdc6 -->
