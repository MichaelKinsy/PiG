### Changed
- `pig mcp login --timeout` now limits the whole sign-in, including requests to the authorization server, instead of only the wait for the browser.

### Fixed
- `/mcp` now opens at once while servers are still connecting, and connection actions (reconnect, enable, disable) run in the background so the manager stays usable. Reconnects and replacements are serialized, direct MCP tool calls wait for their own server's connection, and a call prepared earlier uses the server's current connection when it runs.
- MCP OAuth sign-ins can now be cancelled with Esc at every step, session shutdown stops a running sign-in, and each request to the authorization server times out after 15 seconds. An aborted token refresh no longer falls back to a new authorization.
- Shutdown no longer waits up to 15 seconds to refresh an MCP OAuth token that was about to expire, only to close the server's session. Closing a streamable HTTP session reuses the token of the last request.
- Fixed MCP stdio servers staying alive after PiG exited because the terminal closed, it crashed, or it received a termination signal. Pi ends their process groups from an exit hook; PiG now does the same on those exits.
- Fixed interactive sessions leaving extension prompt sections, such as the MCP `mcp_servers` list, out of the transcript's system message. Print mode and Pi record them; interactive mode now publishes its prompt run to the session, which appends the sections before the first request.
- MCP tool results whose content blocks carry members of another type than the protocol's (a numeric `text`, a `size` string, a missing `uri`, a `null` block, a `resource` that is not an object) now convert as Pi converts them: the model sees what Pi's would, and a block Pi cannot read ends the tool call with Pi's error instead of a placeholder.
