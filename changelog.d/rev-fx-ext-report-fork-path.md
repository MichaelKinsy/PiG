### Fixed

- Fork a `--fork` path argument that is missing, empty, or has no Session header the way Pi does: print `Error: Cannot fork: source session file is empty or invalid: <path>` and exit 1.
