### Fixed

- A chord app's `Close` kept going after a step failed and joined the errors; it now stops at the first failing step and returns that error, as Pi's close chain does (the steps after it, the data directory's lock release included, do not run). <!-- fix: 4477a81c13 -->
