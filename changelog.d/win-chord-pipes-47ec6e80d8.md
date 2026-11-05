### Fixed

- A chord plugin that starts a child process which keeps the Node host's output pipes open (a child that inherited stdout or stderr) no longer holds the host's exit: `Close` and the host's `Exited` waited for that child to end, for as long as it lived. The host now waits for its Node process to exit, and for the end of its output only for the stop grace after that, as Pi's server awaits the child's exit and not its output. <!-- fix: 47ec6e80d8 -->
