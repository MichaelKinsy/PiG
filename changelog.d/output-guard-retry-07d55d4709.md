### Fixed

- Print, JSON and RPC modes: a stdout write that fails with ENOBUFS, EAGAIN or EWOULDBLOCK (a slow or non-blocking pipe) is now retried every 10 ms without losing or repeating bytes, as Pi does; any other write error still ends the process with exit code 1. <!-- fix: 07d55d47099d643bcb624453b45b7d34da7ce7fb -->
