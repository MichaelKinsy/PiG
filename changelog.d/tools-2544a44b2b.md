### Fixed

- A user `!` command whose earlier output was discarded from the rolling window, but whose remaining tail fits the limits, was reported as not truncated, so the model got no truncation notice and no path to the complete log that PiG had already saved. It now reports `truncated` and the path, for a completed and for a cancelled command (D244). <!-- fix: 2544a44b2b -->
