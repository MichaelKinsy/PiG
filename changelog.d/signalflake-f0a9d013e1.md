### Fixed

- An interactive session ended by SIGINT now always exits 130 and leaves its `exit.log` line. The handler was installed only after the first screen was drawn, so a SIGINT in that window killed the session by the default action with no line, and once installed it also asked the session to unwind, which let the process exit 0 before the handler's own exit 130. The handler now goes in first, as Pi registers its signal handlers first in `init()`, and it ends the process without unwinding the session. <!-- fix: f0a9d013e1 -->
