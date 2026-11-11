### Fixed

- The pi-env daemon left its stdin and stdout non-blocking for whoever shared them when it ended by SIGTERM or by its silence limit: it moves them into the runtime poller while it runs, which sets O_NONBLOCK on the open file descriptions a parent shell or a wrapper also holds, and only the normal return gave the mode back. Every exit does now, and a thread-panic message written to a stderr that shares a full stdout pipe waits for room instead of being lost. <!-- fix: bda636eb5 -->
