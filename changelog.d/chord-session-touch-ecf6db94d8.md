### Fixed

- A chord session's `lastUsed` touch was written even when its request had been cancelled; it is now cancelled with the request (a disconnect or the server's close), as Pi's write on the request context, and a request that finished normally still lands it. <!-- fix: ecf6db94d8 -->
