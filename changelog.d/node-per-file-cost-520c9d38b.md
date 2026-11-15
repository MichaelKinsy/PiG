### Changed

- Starting a Node extension for the first time in a process extracts the embedded runtime with one directory step per directory (213) instead of one per file (1871), and the hash of a Go module's sources reads each `.go` file once instead of twice. The extracted tree (paths, bytes, modes) and the digest of `extensions/sdk` are unchanged. On the benchmark rows, allocations per extraction fell from 38,612 to 35,309 (8.6 % fewer, 3 of 3 runs each) and the 16-extension Go content-hash row from 63,678 to 63,421.
