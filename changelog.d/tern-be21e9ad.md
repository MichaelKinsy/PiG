### Fixed

- Fixed the chord host keeping a disposed object reachable until its ready timeout (a minute by default) had passed: a stopped host timer stayed in the runtime's timer heap with its callback, and the callback captured the object. A stopped timer now releases its callback at once. <!-- fix: be21e9ad -->
