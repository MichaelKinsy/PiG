### Changed

- MCP OAuth `clientRegistration: "cimd"` now identifies PiG with its Client ID Metadata Document on PiG's own origin, `https://pi-in-go.dev/oauth/client.json` (or `https://pi-in-go.dev/oauth/<id>/client.json` with `/callback/<id>`), in the Go host and the Node runtime. Pi's own build uses `https://pi.dev/oauth/…`; nothing else differs.
