### Added

- `pig mcp add --oauth-client-registration <mode>` sets `oauth.clientRegistration` (`auto`, `cimd` or `dcr`) for a remote MCP server.

### Changed

- `oauth.clientRegistration` accepts `auto`, the new default. `auto` identifies with Pi's Client ID Metadata Document only when the application is named `pi`; PiG is not, so it keeps registering a client dynamically, as Pi does for rebranded builds. `cimd` still always uses the document, and now fails sign-in only when it was asked for explicitly. A client registered before stays in use under `auto`; `cimd` replaces it.
