### Fixed

- The terminal sandbox starts when the temporary directory has a symlinked ancestor, including macOS's default temporary directory, without expanding its file permissions: the directory is resolved before the sandbox's one-script read grant names it, as Pi does. <!-- fix: a9ff67595 -->
- An oversized WebSocket frame closes with code `1009` on both sides: the socket is left to finish its close handshake after the protocol error instead of being terminated (which reported `1006` and could discard the close frame), as Pi does. <!-- fix: 584ab469e -->
