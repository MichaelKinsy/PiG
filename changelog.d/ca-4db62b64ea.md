### Fixed

- Quitting during startup (Ctrl-D, or Ctrl-C twice, while the tools download or the terminal colours are queried) in a session started with an initial message, such as `pig "/hello"`, ends the session cleanly instead of crashing or starting a prompt with no session bound. <!-- fix: 4db62b64ea -->
