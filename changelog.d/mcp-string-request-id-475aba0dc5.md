### Fixed

- MCP client responses and progress notifications that echo PiG's numeric JSON-RPC request IDs as canonical decimal strings (for example `"1"` for request `1`) now match their pending requests instead of timing out, while rejecting non-canonical forms (`"01"`, `"1.0"`, `"1e0"`, whitespace, negative). <!-- fix: 475aba0dc5 --> <!-- cause: mcp/client.go and mcp/streamable_http.go matched pending request IDs strictly by JSON type without normalizing canonical non-negative integer string IDs -->

### Changed

- Clarified `thinkingLevelMap` documentation and JSON Schema (`models.schema.json`, `models-configuration.md`, `models.md`) to state that `xhigh` and `max` are opt-in and only offered when mapped to a string (`1c177d15d0`).
