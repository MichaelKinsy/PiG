### Upgrading

- Go API `coding/mcpext`: `Host` has a `RegisterTools(definitions []extension.ToolDefinition)` method, which registers a server's tools as one change. A host that has no batch registration can register the definitions one by one. Before: a `Host` implemented `RegisterTool`, `GetAllTools`, `GetActiveTools`, `SetActiveTools` and `GetMcpServers`. After: it also implements `RegisterTools`.

### Fixed

- MCP: a server's tools, the tools it stopped offering, and the resource tools now reach the session as one change, as Pi registers them in one synchronous turn. A prompt that started while a server was connecting could see only some of its tools.
