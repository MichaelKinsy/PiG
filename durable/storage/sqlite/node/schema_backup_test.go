package node

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// writeVersionOneStore writes the store a Pi release before schema version 2 leaves (pi-durable 1.3.0), with next_id 42
// so a copy can be told from a fresh store.
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

// D188: opening a store an older Pi release wrote keeps a copy that release can still open, and never replaces one.
func TestOpenNodeSqliteStorageCopiesAnOlderSchemaBeforeUpgrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "durable.db")
	writeVersionOneStore(t, path)

	openAndClose(t, path)
	backup := path + ".schema-1.bak"
	if version, nextID := storeState(t, backup); version != 1 || nextID != "42" {
		t.Fatalf("backup is version %d with next_id %q, want version 1 with next_id 42", version, nextID)
	}
	if version, nextID := storeState(t, path); version != int64(durablesqlite.CurrentSqliteSchemaVersion) || nextID != "42" {
		t.Fatalf("store is version %d with next_id %q, want version %d with next_id 42", version, nextID, durablesqlite.CurrentSqliteSchemaVersion)
	}

	// An upgraded store is not copied again.
	openAndClose(t, path)
	if _, err := os.Stat(path + ".schema-1-2.bak"); !os.IsNotExist(err) {
		t.Fatalf("an upgraded store was copied again: %v", err)
	}

	// A version 1 store opened again (the user went back to the copy) gets a new copy beside the first.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	writeVersionOneStore(t, path)
	openAndClose(t, path)
	if version, _ := storeState(t, path+".schema-1-2.bak"); version != 1 {
		t.Fatalf("second backup is version %d, want 1", version)
	}
	if version, nextID := storeState(t, backup); version != 1 || nextID != "42" {
		t.Fatalf("first backup changed: version %d, next_id %q", version, nextID)
	}
}

func TestOpenNodeSqliteStorageDoesNotCopyANewOrCurrentStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "durable.db")
	openAndClose(t, path)
	openAndClose(t, path)
	matches, err := filepath.Glob(filepath.Join(dir, "*.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("copies of a new store: %v", matches)
	}
}
