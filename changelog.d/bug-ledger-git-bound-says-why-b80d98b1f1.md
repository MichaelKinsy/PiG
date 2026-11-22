### Fixed

- `make generate` / `make bug-ledger` could fail on a loaded host with a bare `signal: killed` when a git invocation hit the generator's 120s timeout; the hang guard is now 15m and a timeout names the git subcommand, the bound, and that the host or output reader is slow. <!-- fix: b80d98b1f140c12fefa12ae5048d5e1cb360459c --> <!-- cause: repo.git and repo.gitLines used a 120s context timeout and formatted every cmd error without checking context.DeadlineExceeded, surfacing timeouts as bare signal: killed -->
