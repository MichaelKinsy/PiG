### Added

- Piglet manifest key `viewEndpoint` (`none` or `socket`). With `socket`, an interactive session serves the `ui_view` carrier on a Unix socket in the agent directory (`view-endpoint/<pid>.sock`), so a view client outside the process, such as the reference web client, attaches to the same session as the terminal: both show the same mounts, and an intent raised in one is redrawn in both. Only the same user can connect, and a client can draw views and raise their intents and nothing else. Stock PiG and PiG Standard serve no socket.
