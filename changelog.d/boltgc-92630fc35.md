### Changed

- An interactive run collects garbage at a GC percent of 150 until more than 8 MiB of heap is live, then at the default 100 (D621), unless `GOGC` or `GOMEMLIMIT` is set. It cuts the CPU of the interactive and default-theme start-up scenarios by 6-8 % and of a long session by about 3 % per prompt, for about 1.6 MB more peak memory in a short session; a session past 8 MiB live is unchanged. <!-- change: 3f667da78 -->
