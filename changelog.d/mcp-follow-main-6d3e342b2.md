### Fixed

- `tool_search` (and its Durable copy) now finds a tool by property names and descriptions behind a local `$ref`, as in MCP input schemas that put their models in `$defs`; the codemode `resolves to` summary shows field names behind a `$ref`; and a `$ref` with a malformed percent escape renders as `unknown` instead of failing every codemode run. `codemode.SchemaToType`, `RenderToolSignature`, `RenderToolSample`, `RenderDeclarations`, `RenderToolOutputType` and `CreateDescription` no longer return an error. <!-- fix: 6d3e342b2 -->
