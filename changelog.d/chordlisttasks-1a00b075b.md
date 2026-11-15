### Fixed

- Chord: the loading and paging of a list and of a tiling are tasks of the host's loop that waiting for the host to settle counts. Before, they ran on goroutines that only a test helper joined, so a settle could return while a page was still loading. <!-- fix: 1a00b075b6e8b4ab1367070a4b654f4ac19e0f2b -->
