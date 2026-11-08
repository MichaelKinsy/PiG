### Fixed

- Chord: a table read from a handler's goroutine (`handle.Get`, `Page`, `Count`) while its object ended was a data race: the check that the object is still live read the object's documents state outside the host's sections while its end wrote it. The check now takes a section of its own. <!-- fix: 19beefa2ad -->
