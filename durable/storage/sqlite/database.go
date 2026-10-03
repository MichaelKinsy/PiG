// Package sqlite is the portable SQLite storage core of pi-durable: the database facade, the schema migrations, and
// SqliteStorage. It imports no SQLite driver; durable/storage/sqlite/node adapts a concrete connection.
package sqlite

// Ports packages/durable/src/storage/sqlite/database.ts

// SqliteValue is a value supported by the portable SQLite storage core: nil, int64 (or any Go integer), float64,
// string, or []byte.
type SqliteValue = any

// SqliteRow is one result row keyed by column name. INTEGER columns are int64, REAL columns float64, TEXT columns
// string, BLOB columns []byte, and NULL nil.
type SqliteRow = map[string]any

// SqliteExecutor is the SQL surface shared by a database and its transaction handles.
//
// Exec runs SQL text without bindings and may contain several statements. Run, Get, and All execute one statement
// with positional bindings. Adapters may cache prepared statements by SQL text, so callers pass values as bindings
// instead of interpolating them. Get returns a nil row when the statement yields none.
type SqliteExecutor interface {
	Exec(sql string) error
	Run(sql string, params ...SqliteValue) error
	Get(sql string, params ...SqliteValue) (SqliteRow, error)
	All(sql string, params ...SqliteValue) ([]SqliteRow, error)
}

// SqliteDatabase is the minimal database facade required by SqliteStorage.
//
// Every operation blocks until it settles, so adapters may execute outside the caller's goroutine.
//
// Transaction passes the callback a transaction handle. All work in the transaction must use that handle; the
// handle is invalid after the callback returns. Adapters must queue unrelated operations and other transactions
// until the transaction finishes, in call order. Transaction returns after commit or rollback. Calling the database
// itself (including Transaction or Close) from inside a callback therefore waits for that transaction and never
// returns.
//
// When the callback fails, the adapter must roll the transaction back before returning that same error. If rollback
// fails, it must return a different error that does not unwrap to the callback error, so callers cannot mistake the
// callback error for a guaranteed rollback.
type SqliteDatabase interface {
	SqliteExecutor
	Transaction(callback func(transaction SqliteExecutor) error) error
	Close() error
}
