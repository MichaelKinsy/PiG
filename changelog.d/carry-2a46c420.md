### Fixed

- The `--help` text and the `/resume` and `/import` descriptions now say which working directory a resumed session runs in (`--continue` the current directory, `--resume` and `--session` the session's own, `--fork` the current directory), and `/import` says it preserves the session ID and working directory. Carried forward from Pi 2a46c420 (earendil-works/pi#11532), past the 1.4.0+b3a29f11 pin. <!-- fix: 2a46c420 -->
