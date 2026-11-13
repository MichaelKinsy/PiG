### Fixed

- The find tool on Windows returned no results for patterns with Windows separators such as `src\**\*.ts`, and for every path pattern such as `src/**/*.ts` with fd 10.5.1. A Windows path pattern now matches the same way with every fd version: `src/**/*.ts` also finds files directly in `src/`, and `src/*.ts` no longer finds files in subdirectories. <!-- fix: 55a52447fc1 -->
- A find call aborted while the tool looked for a `.git` directory above the search path no longer starts fd. <!-- fix: 55a52447fc1 -->
- The powershell tool no longer reports `Command exited with code 1` under Windows PowerShell 5.1 when a native command succeeded but its stderr was redirected, as in `git push 2>&1 | Select-Object -Last 3`; such commands report the exit code PowerShell 7 reports. <!-- fix: 8ac653e9b23 -->

### Changed

- `GrepOperations.readFile` stays a deprecated, optional member that is accepted and never called, in the Go (`GrepOperations.ReadFile`), Python (`read_file`), Rust (`read_file`) and Lisp (`:read-file`) SDKs as in Pi. Grep takes its context lines from ripgrep; to search a remote system, wrap the tool's `execute`. `pig upgrade` no longer deletes the member.
