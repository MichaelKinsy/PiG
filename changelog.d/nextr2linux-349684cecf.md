### Fixed

- Extension hosts over an IPC or WebSocket link no longer see a message ahead of those that arrived before the listener was set: a hold could reach a relay worker ahead of its hello and end the link with `handshake: hold before hello`. <!-- fix: 349684cecf6ee8bcf6ea7814f3c20b74a75f7274 -->
