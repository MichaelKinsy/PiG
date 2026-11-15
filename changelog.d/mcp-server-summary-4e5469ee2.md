### Added

- `mcp.McpServerSummary(description, instructions)` is a server's one-line summary for the system prompt, as Pi's `mcpServerSummary` of `@earendil-works/pi-mcp`: the first line of the configured description, else of the instructions the server sent in `initialize`. The `mcp_servers` section of the system prompt uses it, so a server whose instructions start with a blank line now shows the first line with text instead of no summary.
