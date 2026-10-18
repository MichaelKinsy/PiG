# Native Windows evidence for portmap-check

State. Four rows (prompt-templates.ts, skills.ts, frontmatter.ts, shell.ts) now derive on the host OS: citations are fixed and the checker runs their evidence tests on macOS or Linux. Their notes name behaviour that only a native Windows test proves (`internal/yaml12/zonelocal_windows_test.go`, `shell_config_windows_test.go`, `taskkill_port_windows_test.go`, `bash_group_*windows_test.go`). The checker runs `go test` on the host, so it never executes a `_windows_test.go` file and a failing Windows test does not turn the row red. The `windows` job of `ci.yml` runs those tests through `automation/ci/windows-native-shard.sh`, but its result is not read by the checker.

Design (not implemented; it needs a CI job and a record the owner must accept).
1. Add to `portmap-mutants.json`-style data a file `test/parity/portmap-windows.json`: Pi file -> list of `dir#TestName` Windows tests.
2. The checker verifies each name is defined in a `*_windows_test.go` file (or a file with a windows build constraint) of a package that `windows-test-packages.sh` selects and that `windows-native-shard.sh` places in a shard. A name that is absent, renamed or not in a shard fails the row.
3. The `windows` job runs `go test -json` for its shard and uploads `windows-test-results.json`. A release-time step (or a CI job on main) writes the passing names with the commit into `test/parity/portmap-windows-record.json`. The checker fails a row when a listed name is not recorded as passed for a record whose digest equals the current digest of the cited files and test package (the same digest `coverage.go` uses).
4. Step 3 makes a Windows test red or stale fail the row. It needs a committed, CI-generated record, so a normal PR cannot tick the row until the Windows job has run on that content. That is the strictness the owner asked for, and also why it is not implemented here: it adds a CI-written file to the repository and a main-branch write path.

Until then the four rows are ticked only because the host evidence derives. The Windows-specific notes stay in the cells.
