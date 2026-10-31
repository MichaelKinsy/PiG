package driver_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/core/store"
	"github.com/MichaelKinsy/PiG/durable/harness"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// The core upgrades a version 1 store (pi-durable 1.3.0) in place, as Pi does, and writes no other file beside it.
func TestOpenHarnessUpgradesAVersionOneStoreInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
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

	database, err = chordnode.OpenNodeSqliteDatabase(path, chordnode.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	row, err := database.Get("SELECT version FROM durable_schema WHERE singleton = 1")
	if err != nil {
		t.Fatal(err)
	}
	if got := row["version"].(int64); got != store.SupportedSchemaVersion {
		t.Fatalf("store is schema version %d, want %d", got, store.SupportedSchemaVersion)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".schema-1.bak"); len(matches) != 0 || !os.IsNotExist(err) {
		t.Fatalf("opening a version 1 store left copies beside it: %v", matches)
	}
}
