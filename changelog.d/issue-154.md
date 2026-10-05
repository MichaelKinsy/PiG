### Fixed

- `pi.exec` in extensions no longer waits for a background process that keeps the command's output open. It returns once the command has exited and its output has been idle for 100 ms, as in Pi.
- `pi.exec` timeout and cancellation now send SIGTERM to the command alone (Pi's behavior), so a SIGTERM handler runs. A command that a signal ended, or on Windows that the kill ended, reports exit code 0, as in Pi.
