### Fixed

- A Chord HTTP request or upgrade no longer reads the router and the authenticators from its own goroutine while a reload composes them. The read recomposed a changed registry beside the commit that changed it: data races, and a reload that failed with "reload inside a commit or a derive compute" (2 of 5 runs of a reload loop beside readers). Pi reads `registries.router.value` on its one JavaScript thread, between the turns of the host's loop; the reads are now sections of the host's loop, which give the same interleaving. <!-- fix: 12d5c56025d8eb244c68ff331d6bbd91ed418d3b -->
