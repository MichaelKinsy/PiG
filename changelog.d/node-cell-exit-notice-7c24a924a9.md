### Fixed

- extensions: when the Node process that several TypeScript or JavaScript extensions share was killed or crashed, an extension could be reported twice: first as disabled (`packed member connection closed; /reload restarts it`), then as crashed and restarting, so one exit read as an error that asked for `/reload` and a warning that the extension restarts by itself. How many notices appeared depended on whether the host noticed the closed connections or the exited process first: two, three or four for two extensions, four in 86 of 100 kills. The host now reports the exit once for each extension, as restarting, in the order the extensions load, whichever it notices first. The extensions of a Go, Python or Rust cell that stops are reported in load order too, where the order varied from one exit to the next. Present in releases 0.3.0 to 0.7.0. <!-- fix: 7c24a924a9f3b784e3748673bb1982fc9bdb7b62 -->

### Known issues

- extensions: when the process of a cell is killed or crashes just before a `/reload` begins, the reload starts the cell again and nothing says that the process had exited. Before the fix above, each extension of a Node cell was reported in that case as disabled (`packed member connection closed; /reload restarts it`), which asked for the `/reload` that was already running.
