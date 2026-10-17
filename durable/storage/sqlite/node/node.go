// Package node opens durable storage on a native SQLite connection. The driver is Chord's (chord/sqlite/node, Pi's
// @earendil-works/chord/sqlite/node); this package re-exports it and adds the durable storage opener, as Pi's
// packages/durable/src/storage/sqlite/node.ts does.
package node

// Ports packages/durable/src/storage/sqlite/node.ts

import (
	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// The Chord driver's names, re-exported as Pi's durable node entry point re-exports them.
type (
	DatabaseSync              = chordnode.DatabaseSync
	DatabaseSyncOptions       = chordnode.DatabaseSyncOptions
	StatementSync             = chordnode.StatementSync
	AggregateError            = chordnode.AggregateError
	NodeSqliteDatabase        = chordnode.NodeSqliteDatabase
	NodeSqliteDatabaseOptions = chordnode.NodeSqliteDatabaseOptions
)

// NewDatabaseSync is Chord's NewDatabaseSync.
func NewDatabaseSync(path string, options DatabaseSyncOptions) (*DatabaseSync, error) {
	return chordnode.NewDatabaseSync(path, options)
}

// NewNodeSqliteDatabase is Chord's NewNodeSqliteDatabase.
func NewNodeSqliteDatabase(database *DatabaseSync) *NodeSqliteDatabase {
	return chordnode.NewNodeSqliteDatabase(database)
}

// OpenNodeSqliteDatabase is Chord's OpenNodeSqliteDatabase.
func OpenNodeSqliteDatabase(path string, options NodeSqliteDatabaseOptions) (*NodeSqliteDatabase, error) {
	return chordnode.OpenNodeSqliteDatabase(path, options)
}

// NodeSqliteStorageOptions is the connection settings plus the durable storage options: the table Prefix (Pi's
// SqliteStorageOptions.prefix) and the working set. A nil field selects its default.
type NodeSqliteStorageOptions struct {
	chordnode.NodeSqliteDatabaseOptions
	// Prefix namespaces every table and index of the durable schema; see sqlite.SqliteStorageOptions.Prefix.
	Prefix *string
	// WorkingSetBytes bounds the decoded entries the storage retains so context reads do not decode the stored history
	// again. It defaults to sqlite.DefaultWorkingSetBytes; 0 retains nothing.
	// pig additive (D104): the working set of decoded entries has no Pi counterpart.
	WorkingSetBytes *int64
}

// OpenNodeSqliteStorage opens or creates file-backed durable storage, optionally under a table prefix. An invalid
// prefix fails before the database or its directory is created.
func OpenNodeSqliteStorage(path string, options NodeSqliteStorageOptions) (*durablesqlite.SqliteStorage, error) {
	if options.Prefix != nil {
		if err := durablesqlite.ValidateSqliteSchemaPrefix(*options.Prefix); err != nil {
			return nil, err
		}
	}
	database, err := chordnode.OpenNodeSqliteDatabase(path, options.NodeSqliteDatabaseOptions)
	if err != nil {
		return nil, err
	}
	retained := durablesqlite.SqliteStorageOptions{Prefix: options.Prefix}
	if options.WorkingSetBytes != nil {
		// SqliteStorageOptions.WorkingSetBytes zero selects the default and a negative value retains nothing.
		retained.WorkingSetBytes = *options.WorkingSetBytes
		if retained.WorkingSetBytes == 0 {
			retained.WorkingSetBytes = -1
		}
	}
	return durablesqlite.Open(database, retained)
}
