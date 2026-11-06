### Fixed

- Windows: the extension host's IPC pipe no longer polls with 1 ms sleeps when a write has to interrupt a pending read or when the pipe is closed; both wait on the operation's own completion. The clipboard open retry ends at once when the command is cancelled. <!-- fix: 285ae70166e1e5275da01c4f82561f08dab3ab91 -->
- Windows: the pipes that carry the output of the bash tool's shell and of `pi.exec` children are overlapped named pipes (as Node's are), and Pig waits for their output on an event instead of polling every millisecond. The child's end of each pipe is still an ordinary synchronous handle, and the order in which stdout and stderr are delivered is unchanged. <!-- fix: aeabf180bee2a3cd31101cd614c2e837389d3d8d -->
