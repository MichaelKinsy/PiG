// Package cloudflare opens durable storage on the SQLite storage of a Cloudflare Durable Object. The driver is Chord's
// (chord/sqlite/cloudflare, Pi's @earendil-works/chord/sqlite/cloudflare); this package re-exports it and adds the durable
// storage opener, as Pi's packages/durable/src/storage/sqlite/cloudflare.ts does.
package cloudflare

// Ports packages/durable/src/storage/sqlite/cloudflare.ts

import (
	chordcloudflare "github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// The Chord driver's names, re-exported as Pi's durable cloudflare entry point re-exports them.
type (
	DurableObjectSqlValue       = chordcloudflare.DurableObjectSqlValue
	DurableObjectSqlCursor      = chordcloudflare.DurableObjectSqlCursor
	DurableObjectSql            = chordcloudflare.DurableObjectSql
	DurableObjectSqliteStorage  = chordcloudflare.DurableObjectSqliteStorage
	DurableObjectSqliteDatabase = chordcloudflare.DurableObjectSqliteDatabase
	RangeError                  = chordcloudflare.RangeError
)

// NewDurableObjectSqliteDatabase is Chord's NewDurableObjectSqliteDatabase.
func NewDurableObjectSqliteDatabase(storage DurableObjectSqliteStorage) *DurableObjectSqliteDatabase {
	return chordcloudflare.NewDurableObjectSqliteDatabase(storage)
}

// OpenDurableObjectSqliteStorage opens durable storage on a SQLite-backed Durable Object, optionally under a table
// prefix (Options.Prefix). Calls given the same storage object share an operation queue.
func OpenDurableObjectSqliteStorage(storage DurableObjectSqliteStorage, options ...durablesqlite.SqliteStorageOptions) (*durablesqlite.SqliteStorage, error) {
	return durablesqlite.Open(NewDurableObjectSqliteDatabase(storage), options...)
}
