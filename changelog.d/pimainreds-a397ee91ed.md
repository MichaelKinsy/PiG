### Fixed

- Stopping and continuing `pig` (Ctrl+Z, then `fg`) while it asks for an API key at the login prompt no longer ends the prompt with `interrupted system call` on macOS: the prompt enters and leaves raw mode through the same retrying calls as the main screen, as Node's `setRawMode` retries the call. <!-- fix: a397ee91ed -->
