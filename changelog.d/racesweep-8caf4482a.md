### Fixed

- p2's MCP plugin no longer crashes the process when its manager ends while it is saving its memento: the write that lost its transaction to the end now fails the handler (reported) instead of panicking with `Cannot use a settled overlay`. <!-- fix: 8caf4482a -->
