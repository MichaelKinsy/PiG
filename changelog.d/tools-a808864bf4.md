### Fixed

- Grep match lines, codemode output and previews, compaction summaries of tool results, and MCP server summaries and connection-error stderr tails no longer cut an emoji or other non-BMP character in half when they truncate: the whole character is dropped, so no lone surrogate reaches the session file, RPC and JSON clients, extensions or providers. `TruncateLine` of the Go, Python, Rust and Lisp SDKs follows, and a negative `maxChars` now keeps nothing of the line (D252). <!-- fix: a808864bf4 -->
