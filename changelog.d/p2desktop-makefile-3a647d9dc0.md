### Added

- The p2 desktop app has a Makefile: `make run` builds and runs it on its own local data directory and `make run-radius` on its own Radius-connected one (signing in on first use), as Pi main 3a647d9dc does for p2-mac; there is no attach target, because the app runs its server in its own process. <!-- feat: p2desktop-makefile -->
