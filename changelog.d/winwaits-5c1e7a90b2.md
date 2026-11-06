### Fixed

- Windows: the extension host's IPC pipe no longer polls with 1 ms sleeps when a write has to interrupt a pending read or when the pipe is closed; both wait on the operation's own completion, which removes a source of stalls and intermittent hangs on shutdown. The clipboard open retry ends at once when the command is cancelled. <!-- fix: 285ae70166e1e5275da01c4f82561f08dab3ab91 -->
