### Fixed

- A second termination signal during print or JSON mode's shutdown now ends the process with the code of the signal that repeated (143 for SIGTERM, 129 for SIGHUP), as Pi's per-signal handler does. A SIGHUP that followed a SIGTERM could end with 143. <!-- fix: 62aaa5e7d9 -->
