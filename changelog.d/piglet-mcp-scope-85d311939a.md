### Fixed

- A Piglet's tool scope now covers the built-in MCP tools. A Piglet with an `extensions:` section that does not list `pig-mcp-adapter` still offered `mcp__<server>__<tool>` from `builtin:mcp`, and `codemode` with them. `ScopeTools` now treats a tool in namespace `mcp__<server>` like the adapter's `mcp:<server>` source: the `pig-mcp-adapter` entry and its `tools` list govern it, and the MCP resource tools follow the same entry by name. Stock PiG without a Piglet is unchanged. <!-- fix: 85d311939a3e56baf0f5ef04d366fac0b3940f3c -->
