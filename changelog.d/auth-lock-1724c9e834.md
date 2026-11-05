### Fixed

- Two pig processes that reach the same `auth.json` through different paths, such as an agent directory whose `auth.json` is a symlink to a shared file, now take one lock instead of two, so they no longer refresh the same single-use OAuth token at the same time and fail with `invalid_grant`. The lock of `auth.json`, `models.json` and `mcp-auth.json` now follows symlinks to the real file, as Pi main does (D236). <!-- fix: 1724c9e834 -->
