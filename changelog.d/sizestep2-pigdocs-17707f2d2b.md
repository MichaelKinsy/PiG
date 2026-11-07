### Changed

- The `pig` binary is about 430 KB smaller (Linux amd64): the 52 pages of the agent docs bundle are embedded as one compressed block instead of 610 KB of plain text. A start no longer hashes the pages to name the bundle directory (the names and the digest are generated with the block), which saves about 1.5 ms of CPU and 39 KB of allocations; `pig docs sync` and the first start of a build decode the block once (about 1.6 ms) and keep nothing of it. The docs bundle on disk is unchanged.
