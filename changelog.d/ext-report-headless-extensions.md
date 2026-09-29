### Fixed

- Bind `ctx.getContextUsage()` to the Session for extensions in print, JSON, and RPC mode, as Pi does in every mode. It previously returned `undefined` outside interactive mode.
