### Fixed

- A pig session on another machine or container that shares your `~/.pig` (an NFS home, a bind mount) is no longer reported as crashed by the next pig you start. Session markers in `crash-output/` record the host and PID namespace, and a marker from another one is left alone instead of being removed with a false "ended without recording an exit" line. <!-- fix: 63d1bf067f -->
