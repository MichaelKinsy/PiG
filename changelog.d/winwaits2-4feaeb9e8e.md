### Fixed

- Windows: a Chord worker process now waits for its parent to accept the connection on its IPC pipe instead of giving up after go-winio's default 2 s dial deadline. A parent that started many workers at once, or a loaded machine, could accept later than that; the worker then had no parent, exited, and its start failed. <!-- fix: 4feaeb9e8e794d057fc708cedbd174b8c9af6f08 -->
