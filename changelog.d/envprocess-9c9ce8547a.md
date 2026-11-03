### Added

- The pi-env daemon's `process` operation runs a program with plain pipes for stdin, stdout and stderr, for programs that speak a protocol on stdin and stdout such as MCP servers: `processWrite` and `processCloseStdin` feed its stdin in arrival order, output arrives as raw bytes per stream, and cancelling or a lost connection kills its process group. `hello` reports it in `features`. `env.OpenProcess` returns a `RemoteProcess`; daemons released before it fail the open with `not_supported`. Unlike terminals, it works on Windows too.
