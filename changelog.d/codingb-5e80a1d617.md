### Changed

- The read tool's streamed read and grep's ripgrep event loop allocate nothing per line or per event: a 140 KB read allocates as often as before it streamed and 41% fewer bytes, and grep's allocations stay constant as its context grows.
