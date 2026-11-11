### Fixed

- Windows: the environment daemon's `taskkill /F /T` runs with the `SystemRoot` it resolved for its path (`C:\Windows` when the variable is empty or unset) instead of inheriting an empty one, with which taskkill did not run and left the killed program's tree alive. <!-- fix: 30cdab4dca7d53826a7c440631c6e6599e90bd71 -->
