package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// Tests of packages/chord/src/next/store/backend.ts: `sqliteBackend(db)` is the backend over exactly the given
// database (the one OpenStore runs its SQL on). Pi has no test file of its own for it.
func TestSqliteBackendRunsOnTheGivenDatabase(t *testing.T) {
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	must(t, err)
	t.Cleanup(func() { _ = database.Close() })
	backend := SqliteBackend(database)
	if backend == nil || backend.DB != database {
		t.Fatalf("SqliteBackend(db).DB = %v, want the given database", backend)
	}
}
