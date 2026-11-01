package node

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// writeVersionOneStore writes the store a Pi release before schema version 2 leaves (pi-durable 1.3.0), with next_id 42
// so an upgraded store can be told from a fresh one.
func writeVersionOneStore(t *testing.T, path string) {
	t.Helper()
	database, err := chordnode.OpenNodeSqliteDatabase(path, chordnode.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := durablesqlite.ApplySqliteMigrations(database, durablesqlite.SqliteMigrations[:1]); err != nil {
		t.Fatal(err)
	}
	if err := database.Run("UPDATE durable_metadata SET next_id = '42' WHERE singleton = 1"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

// storeState is the schema version and next_id of the store at path.
func storeState(t *testing.T, path string) (int64, string) {
	t.Helper()
	database, err := chordnode.OpenNodeSqliteDatabase(path, chordnode.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	schema, err := database.Get("SELECT version FROM durable_schema WHERE singleton = 1")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := database.Get("SELECT next_id FROM durable_metadata WHERE singleton = 1")
	if err != nil {
		t.Fatal(err)
	}
	return schema["version"].(int64), metadata["next_id"].(string)
}

func openAndClose(t *testing.T, path string) {
	t.Helper()
	storage, err := OpenNodeSqliteStorage(path, NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// dirEntries lists the names in dir.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

// Pi's openNodeSqliteStorage upgrades a version 1 store in place (sqlite-migrations.test.ts, "migrates a version 1
// database to store sleeping tasks"): it keeps the data and writes no other file beside the store.
func TestOpenNodeSqliteStorageUpgradesAVersionOneStoreInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "durable.db")
	writeVersionOneStore(t, path)
	before := dirEntries(t, dir)

	openAndClose(t, path)

	if version, nextID := storeState(t, path); version != int64(durablesqlite.CurrentSqliteSchemaVersion) || nextID != "42" {
		t.Fatalf("store is version %d with next_id %q, want version %d with next_id 42", version, nextID, durablesqlite.CurrentSqliteSchemaVersion)
	}
	if after := dirEntries(t, dir); !slices.Equal(after, before) {
		t.Fatalf("opening a version 1 store changed the files beside it: %v, then %v", before, after)
	}
}
