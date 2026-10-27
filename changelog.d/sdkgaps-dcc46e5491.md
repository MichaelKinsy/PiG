### Added

- The Go, Rust and Python SDKs export Pi's `createCodemodeExtension` and `createToolSearchExtension`, and the data options (`logPath`, `startupWaitMs`) of `createMcpExtension`, as references to the compiled-in extensions: pass them in a `DefaultResourceLoader`'s `extensionFactories` and a session built over that loader has the `codemode`, `tool_search` and `/mcp` extensions (the tools inactive until a `tools` option or setting names them).
