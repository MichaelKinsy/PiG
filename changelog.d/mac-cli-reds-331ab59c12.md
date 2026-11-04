### Fixed

- `pig status` reports the agent and project directories by the same resolved path before and after a run creates them. It resolved symbolic links only once a directory existed, so on macOS, where the temporary directory is `/var/...` and resolves to `/private/var/...`, the paths changed between the first run and the next. <!-- fix: 331ab59c12 -->
