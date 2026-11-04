### Fixed

- An MCP Streamable HTTP `application/json` reply over `maxMessageBytes` (default 16 MiB) is now rejected with `MCP JSON response exceeds N bytes`, like an oversized SSE event, instead of being buffered whole (Pi main #11749, D233). <!-- fix: d4e641bde3 -->
- An MCP Streamable HTTP error body is read only up to its first 8 KiB, and the rest is cancelled; the kept excerpt is 8192 bytes, not 8192 UTF-16 units (Pi main #11749, D233). <!-- fix: d4e641bde3 -->
