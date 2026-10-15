### Upgrading

- Go API `durable/storage/sqlite`: `Options` is renamed `SqliteStorageOptions`, the name of Pi's type. Before: `sqlite.Open(db, sqlite.Options{WorkingSetBytes: n})`. After: `sqlite.Open(db, sqlite.SqliteStorageOptions{WorkingSetBytes: n})`.
- Go API `durable/storage/sqlite/node`: the connection settings move to `NodeSqliteDatabaseOptions`, and `NodeSqliteStorageOptions` embeds them next to `Prefix` and `WorkingSetBytes`. `OpenNodeSqliteDatabase` takes `NodeSqliteDatabaseOptions`. Before: `OpenNodeSqliteDatabase(path, node.NodeSqliteStorageOptions{BusyTimeoutMs: &ms})`. After: `OpenNodeSqliteDatabase(path, node.NodeSqliteDatabaseOptions{BusyTimeoutMs: &ms})`. A literal of `NodeSqliteStorageOptions` that sets a connection field names the embedded struct (`NodeSqliteStorageOptions{NodeSqliteDatabaseOptions: node.NodeSqliteDatabaseOptions{BusyTimeoutMs: &ms}}`).
- Go API `durable/storage/sqlite/cloudflare`: `DurableObjectSqlCursor` gains `Next() (row, ok, err)`, which returns the next row; a cursor type implements it next to `ToArray`, which then returns the rows not read yet. `OpenDurableObjectSqliteStorage` takes optional `sqlite.SqliteStorageOptions`.

### Added

- `durable/storage/sqlite`: `SqliteStorageOptions.Prefix` namespaces every table and index of the SQLite storage, for a database shared with host tables (Pi f7e3cb54). `node.OpenNodeSqliteStorage` and `cloudflare.OpenDurableObjectSqliteStorage` forward it; an invalid prefix (not an identifier, or starting with `sqlite_`) fails before the database is created. The default names are unchanged, and changing the prefix does not migrate existing data.

### Changed

- The `kimi-coding` provider uses OpenAI Chat Completions at `https://api.kimi.com/coding/v1` again (Pi a3f265b1 re-lands b06c1f2c, which 6a1f799b had reverted). `models.json` overrides of the `kimi-coding` base URL must include `/v1`.
- Cloudflare Durable Object SQLite adapters made for the same storage object share one operation queue, so several storages can use one Durable Object database. A transaction holds the queue, a statement runs at once while nothing is queued, and `Get` reads only the first row of the cursor (`DurableObjectSqlCursor.Next`).
