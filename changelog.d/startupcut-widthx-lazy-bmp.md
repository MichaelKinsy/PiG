### Changed

- Startup reaches the first interactive frame about 1.2 ms sooner (22.1 ms to 20.9 ms median on a loaded 2-socket Xeon host pinned to one core, n=150 interleaved) with the same screen: the text-width tables filled a 128 KB cache of the Basic Multilingual Plane for the TUI and a second one for the extension SDK before the program started, and now fill a 256-code-point block when the first width lookup lands in it, so a first frame builds a few blocks and a process that never measures a string builds none. Width and wrapping benchmarks are unchanged within noise.
