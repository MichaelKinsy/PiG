### Fixed

- `env/daemon`: `listFiles` with `timeoutMs` 0 truncates at once on Windows too; the deadline check no longer misses on the platform's coarse clock.
