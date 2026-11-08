### Fixed

- `exec` with a timeout reports `killed` when the timeout expired while the host was stalled past both the timeout and the command's exit, as Pi does; it resolved either way at random before. <!-- fix: 99439864e -->
