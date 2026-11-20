### Added

- Opt-in MCP channel delivery (`"channel": true` in `mcp.json` paired with the server's experimental `claude/channel` capability): enabled servers can push `notifications/claude/channel` context that wakes an idle session or queues attributed `mcp-channel` messages FIFO at the steering boundary while keeping server provenance distinct from human user entries.
