package node

// pi: packages/chord/src/sqlite/node.ts

// Ports packages/chord/test/sqlite/node.test.ts (the prepared-statement cache)

import (
	"fmt"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

func TestNodeSqliteStatementCache(t *testing.T) {
	// packages/chord/test/sqlite/node.test.ts (Pi db14038b) "keeps the most recently used prepared statements and prepares evicted ones again"
	t.Run("keeps the most recently used prepared statements and prepares evicted ones again", func(t *testing.T) {
		raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
		mustDo(t, err)
		prepared := 0
		raw.ObservePrepare(func(string) { prepared++ })
		database := NewNodeSqliteDatabase(raw)
		defer func() { mustDo(t, database.Close()) }()
		for i := range 300 {
			_, err := database.Get(fmt.Sprintf("SELECT %d AS n", i))
			mustDo(t, err)
		}
		before := prepared
		row, err := database.Get("SELECT 299 AS n")
		mustDo(t, err)
		expectRow(t, row, chordsqlite.SqliteRow{"n": int64(299)})
		if prepared != before {
			t.Fatalf("prepares after a recent statement = %d, want %d", prepared, before)
		}
		row, err = database.Get("SELECT 0 AS n")
		mustDo(t, err)
		expectRow(t, row, chordsqlite.SqliteRow{"n": int64(0)})
		if prepared != before+1 {
			t.Fatalf("prepares after an evicted statement = %d, want %d", prepared, before+1)
		}
	})
}
