### Fixed

- Start every program PiG runs on Android through `/system/bin/linker64`: `pig install` and `pig remove` for `npm:` and `git:` packages, MCP stdio servers, tools, editors, and the Go, Piglet and git helpers failed with `fork/exec ...: permission denied` on Termux from Google Play.
- Make `pig setup go` on Android say that Go publishes no archive for `android/arm64` and to run `pkg install golang`, and make `pig setup status` give the same next step.
- Let `PIG_CELL_BUILD_TIMEOUT` bound `pig install <dir> --validate-only`, which stopped a slow extension build after a fixed two minutes.
