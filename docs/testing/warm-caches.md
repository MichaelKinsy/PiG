# Warm test caches

Test packages that build real extensions share persistent caches, so a package run compiles only what changed.

- Go: tests keep the user's `GOCACHE` and `GOMODCACHE` (`testenv.ScopeTempDir` pins them before a test replaces `HOME`). A test uses an empty cache only when it tests a cold cache.
- Rust: `coding/extension/host/subprocess`, `coding/cli`, `test/extension-conformance` and the `env` oracle build into `$PIG_TEST_CARGO_CACHE`, else `<user cache dir>/pig-dev/cargo-target`, in a directory named for the package and a hash of the SDK `Cargo.lock` and `Cargo.toml` (`testenv.SharedCargoTargetDir`). A lock change selects a new directory, so stale output cannot be reused. Cargo locks a target directory, so concurrent runs wait for each other. Set `CARGO_TARGET_DIR` to override.

`make test-warm` builds all of these once (slow the first time, a revalidation after). Run it before `make test` or after a lock change. CI restores the Cargo caches and runs it once per Linux job.
