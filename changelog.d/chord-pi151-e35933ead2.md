### Fixed

- The terminal sandbox failed to start when the temporary directory had a symlinked ancestor, such as macOS's default temporary directory. It resolves the directory before starting, as Pi does; the sandbox's read access stays the one script. <!-- fix: a9ff67595 -->
- A WebSocket that receives an oversized frame is left closing while its handlers run, as Pi's is, so the peer receives its 1009 close frame; the server side reports close code 1006, as Pi does. <!-- fix: e35933ead2 -->
