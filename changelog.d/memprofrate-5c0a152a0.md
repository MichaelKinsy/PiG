### Changed

- PiG no longer samples heap allocations for profiling unless `PIG_PROFILE` names `heap` or `allocs` or `GODEBUG` sets `memprofilerate` (D627); a heap profile that another tool takes from a default run is empty. It lowers the memory of a session by about 1 to 2 MB (peak memory of the interactive scenario -1.5 MB, resident memory of a 40-prompt session -1.9 MB) and the user instructions of a `-p` run by 0.8 %; CPU time, wall time and time to interactive did not change. <!-- change: 5c0a152a0 -->
