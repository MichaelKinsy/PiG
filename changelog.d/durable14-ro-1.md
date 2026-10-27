### Changed

- `durable/core/driver` opens a Harness with `HarnessOptions.ReadOnly` instead of refusing it with `driver.ErrUnsupported` (removed): over the core, open loads live tasks as committed (a `running` one stays running), a commit with writes fails with `*durable.ReadOnlySession` before it reaches the store and the Session stays healthy, `Resume()` panics with it, and every call that enables scheduling returns it, as on the Go Harness. The TypeScript host takes `readOnly` in its open options and exports `ReadOnlySession`.

### Fixed

- Closing a Harness over the TypeScript host no longer rejects when the write-back of the tasks the close stopped fails, for example because another process advanced the store. Pi catches that write and the Session reports the failure, so the close resolves. <!-- fix: 98666dfd10d79e195dc65e8002a2e6ea0fede12c -->
