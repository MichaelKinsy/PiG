### Added

- The Go, Rust and Python SDKs serve the function options of Pi's `createMcpExtension` (`loadConfig`, `createTransport`, `credentials`, `openUrl`, `updateConfig`): the functions stay in the extension's process and the host calls them over the connection, so a loader's MCP extension can read its configuration, speak through a transport and keep its OAuth state in the caller's own code.
