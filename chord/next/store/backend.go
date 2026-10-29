package store

import "github.com/MichaelKinsy/PiG/chord/sqlite"

// Ports packages/chord/src/next/store/backend.ts
//
// Store backends (slice 6a §2): what a store runs on. One kind today, any `SqliteDatabase` (Node through
// `chord/sqlite/node`, Durable Objects through `chord/sqlite/cloudflare`); its interface is internal to the store.

// Backend is a backend for OpenStore and ImportStore: opaque to apps, owned by the store once given to it.
type Backend struct {
	// DB is the database the store runs its SQL on.
	DB sqlite.SqliteDatabase
}

// SqliteBackend is a backend over a SQLite database (`OpenNodeSqliteDatabase(path)`, a Durable Object's storage,
// `:memory:` in tests).
func SqliteBackend(db sqlite.SqliteDatabase) *Backend { return &Backend{DB: db} }
