### Changed

- A long interactive session uses less CPU per prompt: each request finds the large tool results it re-sends by their address even when the conversation holds several equal copies (the same file read again), instead of hashing and comparing them on every turn, and the request body goes to the provider connection in vectored writes instead of 32 KiB copies. In the Pi-Bolt long-session benchmark, CPU per prompt at 40 prompts falls from about 81 to 63 ms on one pinned core. The requests sent are byte for byte the same.
