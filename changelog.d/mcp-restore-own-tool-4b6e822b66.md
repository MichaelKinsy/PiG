### Fixed

- MCP tools loaded by `tool_search` are no longer dropped on resume and `/reload` when an extension deactivates only its own tool at `session_start`, as Pi does (upstream #12054). <!-- fix: 4b6e822b66 -->
