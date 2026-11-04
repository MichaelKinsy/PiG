### Fixed

- RPC mode no longer lets a stdin end, a second shutdown request or a single SIGTERM or SIGHUP cut off a shutdown that already started: closing stdin and sending SIGTERM together, as Paseo and systemd do, lets the `session_shutdown` handlers finish, and the process exits with the status of the first trigger. A second signal exits at once with 128 plus its number. Print mode likewise keeps handling signals until its final disposal is done, instead of dying from a signal during it. Pi 1.5.0 behaves as before; PiG follows Pi main ace560c30 (Pi #11599, D231). <!-- fix: 3afef80fa0 -->
