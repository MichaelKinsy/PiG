### Fixed

- A Piglet's tool scope now covers the built-in MCP tools. A Piglet with an `extensions:` section that does not list `pig-mcp-adapter` still offered `mcp__<server>__<tool>` from `builtin:mcp`, and `codemode` with them. `ScopeTools` now treats a tool in namespace `mcp__<server>` like the adapter's `mcp:<server>` source: the `pig-mcp-adapter` entry and its `tools` list govern it, and `codemode`, `tool_search` and the MCP resource tools are hidden when no MCP tool stays visible. Stock PiG without a Piglet is unchanged. <!-- fix: fd68cf8c388aa6885bf02c916d3a6e3f3a86f02a -->
