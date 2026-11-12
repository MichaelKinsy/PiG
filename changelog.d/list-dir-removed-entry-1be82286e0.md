### Fixed

- `listDir` of the local and remote execution environments leaves out an entry removed between enumeration and `lstat` instead of failing, so a read-only JSONL open beside a live writer no longer fails with `JSONL directory listing failed: ENOENT`, as Pi does (upstream #11994, #12055). <!-- fix: 1be82286e0 -->
