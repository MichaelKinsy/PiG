### Added

- Add `make test-changed`, which tests only the packages a change can affect: the packages that hold, embed or name the changed files, and every package whose tests import them. Edited packages run under `-race`. It prints the output of failed tests, including a test still running when its package timed out or crashed, and the slowest tests, and keeps the `go test -json` events in `tmp/test-changed/`. `docs/project/test-economy.md` describes which tier of tests to run when.
- `make test` now fails a test group that leaves a process running after `go test` returns, and kills that process.

### Changed

- `make port-lint` and `make interface-gaps` replay the stored passing run while no tracked, modified or untracked file has changed. Failures always rerun. CI and `PIG_GATE_CACHE=0` always run them.
- Outside CI, `make test` and `make test-changed` report cached results for unchanged packages. Under `CI` every test runs.
- The `make test` cache warm-up compiles with `go list -export` instead of `go build`, so it no longer relinks every command on each run.
- Setting `PIG_NODE_MODULES_STORE` makes `make parity-deps` install each locked Node tree once per host and link checkouts to it.
- The test suite passes when built with `-trimpath`, which lets checkouts share one Go build cache.
