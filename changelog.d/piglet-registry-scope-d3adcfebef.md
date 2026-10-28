### Fixed

- A Piglet's tool scope now bounds the Session's tool registry, not only its active tools. A Piglet that allowed some MCP tools, or hid some tools of an extension, still left the others callable from a `codemode` script (the MCP server logged the call) and loadable with `tool_search`, because both read the registry. The Piglet scope now composes with `--tools` and `--exclude-tools` as an intersection wherever Pi applies its allowlist: definitions, the registry, `getAllTools`, activation and the refresh after a late registration. A hidden tool fails in `codemode` as it does under `--tools` (`tools.<name> does not exist`) and `tool_search` cannot find it. `codemode` and `tool_search` are no longer hidden when a Piglet leaves no MCP tool visible; they just reach nothing hidden. The MCP resource tools (`list_mcp_resources`, `list_mcp_resource_templates`, `read_mcp_resource`) are governed by the `pig-mcp-adapter` entry by name. Stock PiG without a Piglet is unchanged. <!-- fix: d3adcfebef205ccb7b80baa600a973e66e7b4aea -->

### Added

- When a Piglet hides tools, PiG tells you at the first prompt which MCP servers and extension tools it hid and why, `pig piglet show` prints why the Piglet hides MCP tools, and the agent gets one line that names the active Piglet and its tool scope. The Piglet documentation and the MCP documentation have a table of the tool scope rules that the code computes, and a test keeps the table equal to the code.

### Changed

- The "When asked about" list in the system prompt names the Piglet documentation (`docs/piglets.md`). A Piglet that strips `/piglet` does not name it.
