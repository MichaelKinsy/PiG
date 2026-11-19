### Fixed

- Saving `auth.json` (or another state file pig replaces through a temporary file) when the path is not an ordinary file, for example a device such as `/dev/null` or a named pipe, no longer swaps that node for a regular file. pig writes such a target in place, as Pi does, so the bytes reach the device and a full-device error is reported; no temporary file is made for it. <!-- fix: cb4481fdbc --> <!-- cause: the shared writer treated every existing target as a regular file and replaced it by rename, which removed a device or pipe and left an ordinary file with the node's mode. -->
