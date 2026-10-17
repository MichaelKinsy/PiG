package sqlite_test

// pi: packages/chord/src/sqlite/migrations.ts

// Ports packages/chord/test/sqlite/migrations.test.ts

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

func openMemory(t *testing.T) *node.NodeSqliteDatabase {
	t.Helper()
	database, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	return database
}

func expectNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func expectErrorContaining(t *testing.T, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %v, want one containing %q", err, text)
	}
}

func expectRow(t *testing.T, database sqlite.SqliteExecutor, sqlText string, want sqlite.SqliteRow) {
	t.Helper()
	row, err := database.Get(sqlText)
	expectNoError(t, err)
	if !reflect.DeepEqual(row, want) {
		t.Fatalf("%s = %v, want %v", sqlText, row, want)
	}
}

func expectNoSchema(t *testing.T, database sqlite.SqliteExecutor) {
	t.Helper()
	rows, err := database.All("SELECT name FROM sqlite_schema")
	expectNoError(t, err)
	if len(rows) != 0 {
		t.Fatalf("sqlite_schema = %v, want no objects", rows)
	}
}

func TestSqliteMigrations(t *testing.T) {
	// packages/chord/test/sqlite/migrations.test.ts:6
	t.Run("tracks independent histories and skips migrations already applied", func(t *testing.T) {
		database := openMemory(t)
		first := []sqlite.SqliteMigration{
			{Version: 1, Statements: []string{"CREATE TABLE notes (body TEXT)", "INSERT INTO notes VALUES ('kept')"}},
		}
		expectNoError(t, sqlite.ApplySqliteMigrations(database, first, sqlite.ApplySqliteMigrationsOptions{Table: "notes_schema"}))
		expectNoError(t, sqlite.ApplySqliteMigrations(database, first, sqlite.ApplySqliteMigrationsOptions{Table: "notes_schema"}))
		expectNoError(t, sqlite.ApplySqliteMigrations(database, []sqlite.SqliteMigration{}, sqlite.ApplySqliteMigrationsOptions{Table: "other_schema"}))
		expectNoError(t, sqlite.ApplySqliteMigrations(database, append(append([]sqlite.SqliteMigration{}, first...),
			sqlite.SqliteMigration{Version: 2, Statements: []string{"ALTER TABLE notes ADD COLUMN title TEXT"}}), sqlite.ApplySqliteMigrationsOptions{Table: "notes_schema"}))
		rows, err := database.All("SELECT body, title FROM notes")
		expectNoError(t, err)
		if want := []sqlite.SqliteRow{{"body": "kept", "title": nil}}; !reflect.DeepEqual(rows, want) {
			t.Fatalf("notes = %v, want %v", rows, want)
		}
		expectRow(t, database, "SELECT version FROM notes_schema", sqlite.SqliteRow{"version": int64(2)})
		expectRow(t, database, "SELECT version FROM other_schema", sqlite.SqliteRow{"version": int64(0)})
		err = sqlite.ApplySqliteMigrations(database, first, sqlite.ApplySqliteMigrationsOptions{Table: "notes_schema"})
		expectErrorContaining(t, err, "SQLite schema version 2 is newer than supported version 1")
	})

	// packages/chord/test/sqlite/migrations.test.ts:31
	t.Run("rejects invalid migration versions before touching the database", func(t *testing.T) {
		database := openMemory(t)
		for _, versions := range [][]int{{0}, {2}, {1, 3}, {1, 1}} {
			migrations := make([]sqlite.SqliteMigration, len(versions))
			for i, version := range versions {
				migrations[i] = sqlite.SqliteMigration{Version: version, Statements: []string{}}
			}
			expectErrorContaining(t, sqlite.ApplySqliteMigrations(database, migrations, sqlite.ApplySqliteMigrationsOptions{Table: "app_schema"}), "contiguous versions starting at 1")
		}
		expectNoSchema(t, database)
	})

	// packages/chord/test/sqlite/migrations.test.ts:49
	t.Run("validates prefixes and migration table identifiers before executing SQL", func(t *testing.T) {
		database := openMemory(t)
		for _, prefix := range []string{"", "1bad", "bad-prefix", "bad; DROP TABLE notes", "sqlite_app", "SQLITE_app"} {
			if sqlite.ValidateSqliteSchemaPrefix(prefix) == nil {
				t.Fatalf("prefix %q was accepted", prefix)
			}
			if sqlite.ApplySqliteMigrations(database, []sqlite.SqliteMigration{}, sqlite.ApplySqliteMigrationsOptions{Table: prefix}) == nil {
				t.Fatalf("migration table %q was accepted", prefix)
			}
		}
		for _, prefix := range []string{"pi_", "_private", "app2"} {
			expectNoError(t, sqlite.ValidateSqliteSchemaPrefix(prefix))
		}
		expectNoSchema(t, database)
	})

	// packages/chord/test/sqlite/migrations.test.ts:64
	t.Run("rolls initial bootstrap and every pending migration back together", func(t *testing.T) {
		database := openMemory(t)
		failed := []sqlite.SqliteMigration{
			{Version: 1, Statements: []string{
				"CREATE TABLE migration_first (value TEXT) STRICT",
				"INSERT INTO migration_first (value) VALUES ('retained')",
			}},
			{Version: 2, Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT", "THIS IS NOT SQL"}},
		}
		if sqlite.ApplySqliteMigrations(database, failed, sqlite.ApplySqliteMigrationsOptions{Table: "app_schema"}) == nil {
			t.Fatal("failed migration succeeded")
		}
		expectRow(t, database,
			"SELECT count(*) AS count FROM sqlite_schema WHERE name IN ('app_schema', 'migration_first', 'migration_second')",
			sqlite.SqliteRow{"count": int64(0)})
		expectNoError(t, sqlite.ApplySqliteMigrations(database, []sqlite.SqliteMigration{
			failed[0],
			{Version: 2, Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT"}},
		}, sqlite.ApplySqliteMigrationsOptions{Table: "app_schema"}))
		expectRow(t, database, "SELECT version FROM app_schema WHERE singleton = 1", sqlite.SqliteRow{"version": int64(2)})
		expectRow(t, database, "SELECT value FROM migration_first", sqlite.SqliteRow{"value": "retained"})
	})
}
