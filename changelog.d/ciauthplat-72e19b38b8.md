### Fixed

- On Windows the `auth.json`, `models.json` and `mcp-auth.json` lock no longer rewrites the path it locks: an 8.3 short name such as `RUNNER~1` and the case of a directory name stay as given, so a lock error names the path as Pi's does, and an auth path with a `..` step through a directory that does not exist (`./rel/../r2/auth.json`) opens instead of failing with `GetFileAttributesEx`. <!-- fix: 72e19b38b8 -->
