### Fixed

- A changelog version recorded with a prerelease or build suffix, such as `0.1.5-20261020.7`, no longer shows the `0.1.1` to `0.1.5` notes again on every launch: only its leading `major.minor.patch` is read. A recorded version without one shows nothing and is replaced by the installed version instead of showing the whole changelog (Pi #11703, D245). <!-- fix: 3b696c8a7f -->
