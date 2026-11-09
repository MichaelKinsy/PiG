package node

// pi: packages/chord/src/sqlite/node.ts
//
// Ports packages/chord/test/sqlite/node.test.ts "read-only connections" (Pi main c23b9ec1): a read-only connection reads a
// file another connection writes, in deferred transactions each reading one snapshot, and never writes it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

func TestReadOnlyConnectionsReadAFileAnotherConnectionWritesAndNeverWriteIt(t *testing.T) {
	file := filepath.Join(t.TempDir(), "data.sqlite")
	writer, err := OpenNodeSqliteDatabase(file, NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Exec("CREATE TABLE probe (value INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Run("INSERT INTO probe VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenNodeSqliteDatabase(file, NodeSqliteDatabaseOptions{Mode: NodeSqliteReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	// One snapshot per transaction: a write committed meanwhile shows in the next one.
	var before, during []chordsqlite.SqliteRow
	err = reader.Transaction(func(tx chordsqlite.SqliteExecutor) error {
		var err error
		if before, err = tx.All("SELECT value FROM probe"); err != nil {
			return err
		}
		if err = writer.Run("INSERT INTO probe VALUES (2)"); err != nil {
			return err
		}
		during, err = tx.All("SELECT value FROM probe")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || len(during) != 1 {
		t.Fatalf("snapshot: before %v during %v", before, during)
	}
	after, err := reader.All("SELECT value FROM probe")
	if err != nil || len(after) != 2 {
		t.Fatalf("after = %v, %v", after, err)
	}
	if err := reader.Run("INSERT INTO probe VALUES (3)"); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Fatalf("write error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if missing, err := OpenNodeSqliteDatabase(filepath.Join(t.TempDir(), "missing.sqlite"), NodeSqliteDatabaseOptions{Mode: NodeSqliteReadOnly}); err == nil {
		_ = missing.Close()
		t.Fatal("a missing file opened read-only")
	}
}

// pi: packages/chord/src/sqlite/node.ts
//
// NodeSqliteDatabase's constructor takes the mode as its second argument and defaults it to "read-write" (node.ts close():
// `if (this.mode === "read-write") PRAGMA wal_checkpoint(TRUNCATE)`): closing a read-write adapter empties the WAL the other
// connection left, closing a read-only one checkpoints nothing.
func TestNewNodeSqliteDatabaseTakesPisModeAndDefaultsToReadWrite(t *testing.T) {
	walSize := func(mode NodeSqliteMode) int64 {
		file := filepath.Join(t.TempDir(), "data.sqlite")
		never := 0
		writer, err := OpenNodeSqliteDatabase(file, NodeSqliteDatabaseOptions{WalAutoCheckpointPages: &never})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = writer.Close() }()
		if err := writer.Exec("CREATE TABLE probe (value INTEGER)"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Run("INSERT INTO probe VALUES (1)"); err != nil {
			t.Fatal(err)
		}
		raw, err := NewDatabaseSync(file, DatabaseSyncOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := NewNodeSqliteDatabase(raw, mode).Close(); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(file + "-wal")
		if err != nil {
			t.Fatal(err)
		}
		return info.Size()
	}
	for _, mode := range []NodeSqliteMode{"", NodeSqliteReadWrite} {
		if size := walSize(mode); size != 0 {
			t.Fatalf("mode %q: WAL holds %d bytes after close, want it truncated", mode, size)
		}
	}
	if size := walSize(NodeSqliteReadOnly); size == 0 {
		t.Fatal("read-only mode: close checkpointed the WAL")
	}
}
