package sqlite_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

func tableCount(t *testing.T, database sqlite.SqliteExecutor, name string) int {
	t.Helper()
	row := must(database.Get("SELECT count(*) AS count FROM sqlite_schema WHERE type = 'table' AND name = ?", name))
	return int(row["count"].(int64))
}

// migrations.ts applySqliteMigrations(database, migrations = SQLITE_MIGRATIONS) applies exactly the list it is given: an omitted list is the default schema's
// history, an empty list is no history. applySqliteMigrationsForSchema(database, schema, migrations = createSqliteMigrations(schema)) renders the history for
// its schema and keeps the schema's own version table; both await the migration to its end and fail with its failure (TestDurableSQLiteMigrations).
func TestSqliteMigrationEntryPointsApplyTheListTheyAreGiven(t *testing.T) {
	t.Run("an empty list applies nothing and an omitted list applies the default history", func(t *testing.T) {
		database := openDatabase(t, databasePath(t))
		defer func() { mustDo(t, database.Close()) }()
		mustDo(t, sqlite.ApplySqliteMigrations(database, []sqlite.SqliteMigration{}))
		if n := tableCount(t, database, "durable_metadata"); n != 0 {
			t.Fatalf("an empty migration list created the schema (%d tables)", n)
		}
		mustDo(t, sqlite.ApplySqliteMigrations(database, nil))
		if n := tableCount(t, database, "durable_metadata"); n != 1 {
			t.Fatalf("an omitted migration list left %d metadata tables", n)
		}
	})
	t.Run("a prefixed schema renders its own history under its own version table", func(t *testing.T) {
		database := openDatabase(t, databasePath(t))
		defer func() { mustDo(t, database.Close()) }()
		prefix := "p_"
		schema := must(sqlite.CreateSqliteSchema(&prefix))
		mustDo(t, sqlite.ApplySqliteMigrationsForSchema(database, schema, nil))
		expectRow(t, database, "SELECT version FROM "+schema.Tables.DurableSchema+" WHERE singleton = 1", sqlite.SqliteRow{"version": sqlite.CurrentSqliteSchemaVersion})
		if n := tableCount(t, database, "durable_schema"); n != 0 {
			t.Fatalf("the default version table exists beside the prefixed schema (%d)", n)
		}
		if n := tableCount(t, database, schema.Tables.DurableMetadata); n != 1 {
			t.Fatalf("the prefixed metadata table is missing (%d)", n)
		}
	})
}
