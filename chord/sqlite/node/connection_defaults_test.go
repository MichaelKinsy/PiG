package node

// pi: packages/chord/src/sqlite/node.ts

import (
	"reflect"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Ports "configures connection defaults and accepts explicit settings" of packages/chord/test/sqlite/node.test.ts.
func TestNodeSqliteConfiguresConnectionDefaultsAndAcceptsExplicitSettings(t *testing.T) {
	busy, checkpoint := 123, 0
	for _, options := range []struct {
		name       string
		options    NodeSqliteDatabaseOptions
		busy       int64
		checkpoint int64
	}{
		{"defaults", NodeSqliteDatabaseOptions{}, 5_000, 1_000},
		{"explicit", NodeSqliteDatabaseOptions{BusyTimeoutMs: &busy, WalAutoCheckpointPages: &checkpoint}, 123, 0},
	} {
		t.Run(options.name, func(t *testing.T) {
			database, err := OpenNodeSqliteDatabase(":memory:", options.options)
			mustDo(t, err)
			defer func() { mustDo(t, database.Close()) }()
			for pragma, want := range map[string]chordsqlite.SqliteRow{
				"PRAGMA busy_timeout":       {"timeout": options.busy},
				"PRAGMA wal_autocheckpoint": {"wal_autocheckpoint": options.checkpoint},
				"PRAGMA synchronous":        {"synchronous": int64(1)},
			} {
				rows, err := database.All(pragma)
				mustDo(t, err)
				if len(rows) != 1 || !reflect.DeepEqual(rows[0], want) {
					t.Fatalf("%s = %v, want %v", pragma, rows, want)
				}
			}
		})
	}
}
