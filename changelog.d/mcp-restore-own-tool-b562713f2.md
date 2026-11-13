### Fixed

- MCP tools loaded by `tool_search` are no longer dropped on resume and `/reload` when an extension deactivates only its own tool at `session_start`, as Pi does (upstream #12054). <!-- fix: b562713f26fef66342f709edca8904ede4f1bdf6 -->
