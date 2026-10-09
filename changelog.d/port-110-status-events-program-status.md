### Added

- Added program status reporting with OSC 7501: terminals and agent dashboards that support it see whether PiG is working, blocked on a dialog or login, done, or failed. `PI_PROGRAM_STATUS=1|0` overrides detection (see [Terminal setup](docs/site/docs/terminal-setup.md#program-status)). The reported `app` is `pig`.

### Fixed

- Fixed the error message of a failed lazy API setup, such as a module load or auth failure, using its failure time as `timestamp` instead of the request start.
