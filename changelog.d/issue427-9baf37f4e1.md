### Fixed

- MCP stdio servers now start in a session of their own, as Pi's stdio transport does (`detached` in Node's spawn is `setsid`): a server, or a child of it, no longer keeps PiG's controlling terminal and cannot take the terminal's foreground process group from PiG. The server still leads its own process group, so closing the transport still ends its children (#427). <!-- fix: 9baf37f4e1 -->
