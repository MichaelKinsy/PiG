### Fixed

- `listDir` of the local and remote execution environments leaves out an entry removed between enumeration and `lstat` instead of failing, so a read-only JSONL open beside a live writer no longer fails with `JSONL directory listing failed: ENOENT`, as Pi does (upstream #11994, #12055). <!-- fix: e6e4f9acc50b26111b6f83f48ea004ca1fa9927e -->
