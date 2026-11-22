### Fixed

- On macOS and the BSDs, after a Chord server is hard-killed in the middle of an import or delete, the next start now undoes the interrupted import or finishes the interrupted delete once the dead server's stale lock file is removed (as in Pi); previously the address stayed blocked on its own stale lock file. <!-- fix: 0024f22d1e --> <!-- cause: startup intent recovery was written and tested where process locks are Linux abstract sockets that disappear when their holder dies, so the start never cleared a stale socket-file address lock before taking it -->
