### Fixed

- CI: `TestTerminalProgramGetsDefaultSignalsWhenTheDaemonStartedIgnoringThem` no longer fails every run under `make test-fast`. It required every ignored-signal bit of the terminal's program to be clear, but Pi's `pty.rs` (and PiG's daemon, which mirrors it) resets signals 1 through 31 only, and GNU make 4.4.1 starts its recipes with signals 32 and 33 ignored, so the program ended with `SigIgn` 0x80000000 under both daemons. The test asserts signals 1 through 31, the daemon's contract. <!-- fix: abb8c1dc6d -->
