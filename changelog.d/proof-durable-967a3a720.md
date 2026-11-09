### Changed

- Go: `chord/sqlite/node` and `durable/storage/sqlite/node` take Pi's `mode` as `NodeSqliteDatabaseOptions.Mode` (`NodeSqliteReadWrite`, `NodeSqliteReadOnly`) and as the second argument of `NewNodeSqliteDatabase`, replacing the `ReadOnly` option; an empty mode is read-write as before. `durable.CompactionOverrides.Get` returns the override and whether the model has an entry, as indexing Pi's `modelOverrides` object does. A read-only connection and the compaction settings behave as before.
