# PiG fork of the modernc.org/sqlite v1.62.0 driver layer

This directory is the Go driver layer of `modernc.org/sqlite` v1.62.0 (BSD-3-Clause, `LICENSE`): the non-test Go files of the module's root package (the `database/sql/driver` implementation: connection, statements, rows, transactions, DSN handling, hooks, page-cache and virtual-table glue). It is a package of the root module, `github.com/MichaelKinsy/PiG/internal/third_party/sqlite` (a `replace` directive would make `go install github.com/MichaelKinsy/PiG/cmd/pig@latest` fail). The transpiled SQLite library is not copied: the package still imports `modernc.org/sqlite/lib` (and `modernc.org/sqlite/vtab`, `modernc.org/libc`) from the module versions in `go.mod`. Only `chord/sqlite/node` imports this package.

## Why

modernc opens every connection with `SQLITE_OPEN_FULLMUTEX`, so SQLite takes a per-connection mutex on every call into it. `chord/sqlite/node` drives one connection per handle from one goroutine at a time (its serial queue), so the mutex is pure cost: opening with `SQLITE_OPEN_NOMUTEX` measured the Durable read benchmarks -7.9% (geomean; `handoffs/next/DurableSqlitePerf-nomutex.patch` is the one-line form). The upstream change is a DSN parameter; until it is merged and released, this copy carries it.

Upstream: modernc DSN nomutex PR (owner to post); draft: `~/PiG-launch-newer/handoffs/next/modernc-dsn-nomutex-pr.md`. When it is released, delete this directory, point `chord/sqlite/node` at `modernc.org/sqlite` again (the DSN parameter is the same) and drop the notice entries.

## Differences from v1.62.0

1. `threading.go` (new): `getThreadingMode`, the `_threading_mode` DSN parameter (`serialized`, the default and the previous behaviour, or `multi-thread`).
2. `conn.go`: `newConn` reads `_threading_mode` and opens with the flag it selects instead of the fixed `SQLITE_OPEN_FULLMUTEX`.
3. `driver.go`, `doc.go`: the DSN and the `sql.Conn.Raw` paragraph document the parameter.
4. `sqlite.go`: `init` does not `sql.Register("sqlite", ...)`, so the package does not collide with `modernc.org/sqlite` in a binary that links both (`bench/durableperf`, tests). `NewConnector` is the entry point.
5. Every `package sqlite // import "modernc.org/sqlite"` import comment is `package sqlite`.
6. `threading_test.go` (new): the DSN selects the mutex mode (`sqlite3_db_mutex` is NULL for `multi-thread`) and rejects bad values. The package's own upstream tests are not copied.

`chord/sqlite/node/threading_test.go` enforces the other half of the contract: every driver call on a connection of a `NodeSqliteDatabase`, under concurrent use from many goroutines, starts only after the previous one returned (run it with `-race`).

## Updating

Copy the new upstream release's root-package non-test files over this directory, repeat the differences above and run `go test ./internal/third_party/sqlite ./chord/sqlite/node`. Offer nothing upstream without the owner.
