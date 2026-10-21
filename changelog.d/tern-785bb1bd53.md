### Fixed

- Forking a session from a file no longer overwrites a session file that already has the new session's timestamp and id. The fork now fails with `EEXIST: file already exists` and leaves the file untouched, as Pi does, instead of truncating it. <!-- fix: 785bb1bd53 -->
