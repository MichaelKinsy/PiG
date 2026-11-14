### Fixed

- A Chord host's IPC or WebSocket link no longer hands its listener a message that arrived while earlier queued messages were still being handed over ahead of them: a hold could reach a relay worker before its hello and end the link with `handshake: hold before hello`. <!-- fix: 4fc2ad082d033ddb9a3ace79c40a76e656aef0f3 -->
