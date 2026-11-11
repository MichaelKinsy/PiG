### Fixed

- p2: a subagent with code mode gets the tools of an MCP server that connects after it was spawned (`enableScriptTools` tested the agent state's tool list with the wrong type and never added them), so its scripts can call them. <!-- fix: e7ad444950afe73d57f1f9f2afe0b7c906c860e0 -->
