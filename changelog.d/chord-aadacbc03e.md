### Fixed

- Chord's terminal launcher no longer ends the session when a stop and continue (Ctrl+Z, then `fg`) interrupts the termios ioctl of raw mode: macOS reports that as EINTR, and Node's `setRawMode` retries it, but the launcher's `SetRaw` called `x/term` directly and failed with `interrupted system call`. Entering and leaving raw mode now retries the ioctls on EINTR. <!-- fix: ff6b95eb25, aadacbc03e -->
