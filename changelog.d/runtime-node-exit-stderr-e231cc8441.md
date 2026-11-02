### Fixed

- A Node extension that fails to start or crashes no longer dies without saying why. The runtime wrote its error to stderr and exited in the same turn, and because PiG reads that stderr through a pipe, Node could drop a long message (a stack trace past the pipe's 64 KiB buffer) before it was sent. The runtime now exits once the message has been written. <!-- fix: e231cc8441 -->
