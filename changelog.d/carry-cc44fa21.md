### Fixed

- `tool_search` and codemode `searchTools()` no longer rank a tool below tools that only mention its name in their descriptions, for example `peekaboo app` ranking `mcp__peekaboo__app` 14th. A query word in a tool's name now scores more than any number of mentions in its description, a tool whose whole name is in the query gets a large bonus, and long server instructions no longer lower the scores of that server's tools. Carried forward from Pi cc44fa21 (earendil-works/pi#11331), past the 1.4.0+b3a29f11 pin. <!-- fix: cc44fa21 -->
