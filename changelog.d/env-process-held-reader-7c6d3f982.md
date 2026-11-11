### Fixed

- The pi-env daemon no longer loses the end of a program's output when the program exits while the link is backed up: `process` (and the terminal) ended their wait 100 ms after the exit even though a reader still waited for the link with output left in the pipe, so up to one pipe buffer went missing. Reading now continues while a reader is held (Pi b48c675a912f). <!-- fix: 7c6d3f98216e83d30b07ae3b33829f84d397f4fb -->
