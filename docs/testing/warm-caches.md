# Warm test caches

Test packages that build real extensions share persistent caches, so a package run compiles only what changed.

- Go: tests keep the user's `GOCACHE` and `GOMODCACHE` (`testenv.ScopeTempDir` pins them before a test replaces `HOME`). A test uses an empty cache only when it tests a cold cache.
- Rust: `coding/extension/host/subprocess`, `coding/cli`, `test/extension-conformance` and the `env` oracle build into `$PIG_TEST_CARGO_CACHE`, else `<user cache dir>/pig-dev/cargo-target`, in a directory named for the package and a hash of the SDK `Cargo.lock` and `Cargo.toml` (`testenv.SharedCargoTargetDir`). A lock change selects a new directory, so stale output cannot be reused. Cargo locks a target directory, so concurrent runs wait for each other. Set `CARGO_TARGET_DIR` to override.

`make test-warm` builds all of these once (slow the first time, a revalidation after). Run it before `make test` or after a lock change. CI restores the Cargo caches and runs it once per Linux job.

A test that drives a real terminal session builds its extension before the session starts: a print-mode run in the same `PIG_HOME` (`prebuildExtension` in `coding/cli/extension_sprite_pty_test.go`) fills the build cache, and the session finds it. Inside the session the build comes before the header, so a wait for the header would cover a build that waits for another build's Cargo lock (a run on the same host that shares the cache root holds it too) or compiles a cold target, and no `testbudget` bound can say how long that takes. The print-mode run waits for the process to exit; a build that never ends fails at the `go test` deadline.

A build that no event of the test decides ends at the `go test` deadline, not at a fixed bound: `testbudget.DeadlineContext` bounds the shared pig binary of `coding/cli` (`build_binary_test.go`) and the Rust load of `TestContextResultShapesCrossSDKTransports`, and a step cut there says so (`testbudget.Expired`). A starved host then fails with the deadline named instead of with a killed `go` or `cargo`.
