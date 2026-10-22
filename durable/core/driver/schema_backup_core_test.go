package driver_test

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/harness"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// D188: the core keeps a copy of a store an older Pi release wrote (pi-durable 1.3.0, schema version 1), which that
// release can still open, before it upgrades the store.
func TestOpenHarnessCopiesAnOlderSchemaBeforeUpgrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	database, err := chordnode.OpenNodeSqliteDatabase(path, chordnode.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := durablesqlite.ApplySqliteMigrations(database, durablesqlite.SqliteMigrations[:1]); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	h, err := driver.OpenHarness(t.Context(), path, harness.HarnessOptions{Models: ai.CreateModels(), Registry: harness.CreateRegistry()})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	version := func(path string) int64 {
		t.Helper()
		database, err := chordnode.OpenNodeSqliteDatabase(path, chordnode.NodeSqliteDatabaseOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = database.Close() }()
		row, err := database.Get("SELECT version FROM durable_schema WHERE singleton = 1")
		if err != nil {
			t.Fatal(err)
		}
		return row["version"].(int64)
	}
	if got := version(path + ".schema-1.bak"); got != 1 {
		t.Fatalf("backup is schema version %d, want 1", got)
	}
	if got := version(path); got != store.SupportedSchemaVersion {
		t.Fatalf("store is schema version %d, want %d", got, store.SupportedSchemaVersion)
	}
}
