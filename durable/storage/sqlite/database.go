// Package sqlite is the portable SQLite storage core of pi-durable: the durable schema, its migrations, and
// SqliteStorage. The database facade and the migration runner are Chord's (chord/sqlite); durable/storage/sqlite/node
// and durable/storage/sqlite/cloudflare open storage on Chord's drivers.
package sqlite

// Ports packages/durable/src/storage/sqlite/database.ts

import chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"

// The Chord facade's names, re-exported as Pi's durable database entry point re-exports them.
type (
	SqliteValue    = chordsqlite.SqliteValue
	SqliteRow      = chordsqlite.SqliteRow
	SqliteExecutor = chordsqlite.SqliteExecutor
	SqliteDatabase = chordsqlite.SqliteDatabase
	TextReader     = chordsqlite.TextReader
)
