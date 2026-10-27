### Added

- `durable.HarnessOptions.ReadOnly` opens a Harness as a viewer of a session another process drives: open leaves running tasks as committed instead of recovering them, every commit with writes fails with the new `durable.ReadOnlySession` before it reaches Storage (the Session stays healthy, and a commit that writes nothing succeeds), `Resume()` panics with it and every call that enables scheduling returns it, `Wake()` is always inactive and `OnWake` is never called. Reads, snapshots and watches keep working; reopen without `ReadOnly` to write. Ports Pi's `HarnessOptions.readOnly`.
