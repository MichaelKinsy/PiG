### Fixed

- env: closing a connection at the moment its daemon was being started left that daemon process unreaped, a zombie holding its pipes, until PiG exited. The start that finds the connection closed now waits for the daemon it has just signalled. <!-- fix: a3391bc58e0bed3079666cbb6665373cba45f0fe -->
