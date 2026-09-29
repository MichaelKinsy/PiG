### Fixed

- Start Node extension processes with the host's resolved terminal capabilities, as Pi's extensions share its process-wide capability cache (Pi 0.87.1 `terminal-image.ts:34,160-169`; `main.ts:853,887`). Under tmux, a Node extension no longer runs a second synchronous `tmux display-message` probe before it loads, and its factory sees the host terminal's capabilities.
