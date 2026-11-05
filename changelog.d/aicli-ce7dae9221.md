### Fixed

- MCP servers that frame server-sent events with a lone CR (or a CRLF split across reads) now deliver their events, as in Pi; the call used to fail with "stream ended without a response". <!-- fix: ce7dae9221 -->
