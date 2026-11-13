### Fixed

- `pi.exec`: when a command's output exceeds `maxBuffer`, the output pipes are now closed before the command is signalled, as Pi does. PiG signalled first, so a command that was still writing always reported code 143; it now reports 141 when its next write met the closed pipe first (SIGPIPE) and 143 when the SIGTERM came first, as Pi does. A command that had already exited keeps its own code, 1 for a 0. <!-- fix: 6da170c870d6af4344932b372354f9d94d6d4b7f -->
