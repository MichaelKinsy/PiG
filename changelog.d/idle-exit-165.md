### Fixed

- A panic in a background goroutine of an interactive session no longer ends PiG with only a stderr dump. PiG restores the terminal, prints the error, records it in `crashes.json` and exits 1, as Pi does for an uncaught exception. Any other goroutine panic or runtime fatal error is copied to `<agent dir>/crash-output/` and becomes a crash record at the next start.
- The prompt-cache refresh that fires minutes after a prompt now treats a panic in the extension decision or in the refresh request as Pi treats a thrown error there: it keeps Pi's decision or skips that refresh and keeps warming, instead of ending the session.
- When the terminal's input ends while its output is still there, PiG keeps running as Pi does instead of exiting 1 with `Error: EOF`.

### Added

- `<agent dir>/exit.log` records every interactive exit that no user action asked for, with the time, pid and parent pid: SIGHUP, SIGTERM, an external SIGINT, a closed terminal, an extension's `shutdown()` and a failed interactive run. A session that the OS killed (SIGKILL, memory pressure, power loss) is reported at the next start. `pig diagnose` prints the crash log, exit log and debug log paths and the newest exit lines.
