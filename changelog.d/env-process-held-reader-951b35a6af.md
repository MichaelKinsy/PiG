### Fixed

- The pi-env daemon no longer loses the end of a program's output when the program exits while the link is backed up: `process` (and the terminal) ended their wait 100 ms after the exit even though a reader still waited for the link with output left in the pipe, so up to one pipe buffer went missing. Reading now continues while a reader is held (Pi b48c675a912f). <!-- fix: 951b35a6af82cdc47099cec0e5b0ef99e3e30f11 -->
