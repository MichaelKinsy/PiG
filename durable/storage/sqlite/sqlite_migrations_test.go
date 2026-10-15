package sqlite_test

// pi: packages/durable/src/storage/sqlite/migrations.ts

// Ports packages/durable/test/sqlite-migrations.test.ts

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
	"github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

func databasePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "storage.sqlite")
}

func openDatabase(t *testing.T, path string) *node.NodeSqliteDatabase {
	t.Helper()
	return must(node.OpenNodeSqliteDatabase(path, node.NodeSqliteStorageOptions{}))
}

func expectRow(t *testing.T, database sqlite.SqliteExecutor, sqlText string, expected sqlite.SqliteRow) {
	t.Helper()
	expectEqual(t, must(database.Get(sqlText)), expected)
}

func TestDurableSQLiteMigrations(t *testing.T) {
	t.Run("creates the current schema and can be applied repeatedly", func(t *testing.T) {
		database := openDatabase(t, databasePath(t))
		defer func() { mustDo(t, database.Close()) }()
		mustDo(t, sqlite.ApplySqliteMigrations(database, nil))
		mustDo(t, sqlite.ApplySqliteMigrations(database, nil))
		expectRow(t, database, "SELECT version FROM durable_schema WHERE singleton = 1",
			sqlite.SqliteRow{"version": sqlite.CurrentSqliteSchemaVersion})
		expectRow(t, database, "SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1",
			sqlite.SqliteRow{"next_id": "2", "next_seq": 1})
	})

	t.Run("rejects a database newer than the portable core", func(t *testing.T) {
		path := databasePath(t)
		database := openDatabase(t, path)
		mustDo(t, sqlite.ApplySqliteMigrations(database, nil))
		mustDo(t, database.Run("UPDATE durable_schema SET version = ? WHERE singleton = 1", sqlite.CurrentSqliteSchemaVersion+1))
		mustDo(t, database.Close())

		_, err := node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{})
		expectError(t, err, "is newer than supported version")
	})

	t.Run("rolls initial bootstrap and every pending migration back together", func(t *testing.T) {
		database := openDatabase(t, databasePath(t))
		defer func() { mustDo(t, database.Close()) }()
		failed := []sqlite.SqliteMigration{
			{Version: 1, Statements: []string{
				"CREATE TABLE migration_first (value TEXT) STRICT",
				"INSERT INTO migration_first (value) VALUES ('retained')",
			}},
			{Version: 2, Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT", "THIS IS NOT SQL"}},
		}
		if err := sqlite.ApplySqliteMigrations(database, failed); err == nil {
			t.Fatal("failed migration succeeded")
		}
		expectRow(t, database,
			"SELECT count(*) AS count FROM sqlite_schema WHERE name IN ('durable_schema', 'migration_first', 'migration_second')",
			sqlite.SqliteRow{"count": 0})

		mustDo(t, sqlite.ApplySqliteMigrations(database, []sqlite.SqliteMigration{
			failed[0],
			{Version: 2, Statements: []string{"CREATE TABLE migration_second (value TEXT) STRICT"}},
		}))
		expectRow(t, database, "SELECT version FROM durable_schema WHERE singleton = 1", sqlite.SqliteRow{"version": 2})
		expectRow(t, database, "SELECT value FROM migration_first", sqlite.SqliteRow{"value": "retained"})
	})

	t.Run("rolls a failed migration back and preserves stored data for a successful retry", func(t *testing.T) {
		path := databasePath(t)
		storage := must(node.OpenNodeSqliteStorage(path, node.NodeSqliteStorageOptions{}))
		commit(t, storage,
			durable.ConversationWrite{Value: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}},
			durable.EntryWrite{Value: durable.EntryRecord{
				Id:             durable.IdFromNumber[durable.EntryId](2),
				ConversationId: durable.ROOT_CONVERSATION_ID,
				Kind:           "retained",
				Data:           map[string]any{"retained": true},
			}},
		)
		mustDo(t, storage.Close(testContext))

		database := openDatabase(t, path)
		nextVersion := sqlite.CurrentSqliteSchemaVersion + 1
		failedMigrations := append(append([]sqlite.SqliteMigration{}, sqlite.SqliteMigrations...), sqlite.SqliteMigration{
			Version: nextVersion, Statements: []string{"CREATE TABLE migration_probe (value TEXT) STRICT", "THIS IS NOT SQL"},
		})
		if err := sqlite.ApplySqliteMigrations(database, failedMigrations); err == nil {
			t.Fatal("failed migration succeeded")
		}
		expectRow(t, database, "SELECT version FROM durable_schema WHERE singleton = 1",
			sqlite.SqliteRow{"version": sqlite.CurrentSqliteSchemaVersion})
		expectRow(t, database,
			"SELECT count(*) AS count FROM sqlite_schema WHERE type = 'table' AND name = 'migration_probe'",
			sqlite.SqliteRow{"count": 0})

		successfulMigrations := append(append([]sqlite.SqliteMigration{}, sqlite.SqliteMigrations...), sqlite.SqliteMigration{
			Version: nextVersion, Statements: []string{"CREATE TABLE migration_probe (value TEXT) STRICT"},
		})
		mustDo(t, sqlite.ApplySqliteMigrations(database, successfulMigrations))
		expectRow(t, database, "SELECT version FROM durable_schema WHERE singleton = 1", sqlite.SqliteRow{"version": nextVersion})
		expectRow(t, database, "SELECT record, commit_seq FROM entries WHERE id = 2", sqlite.SqliteRow{
			"record":     `{"id":2,"conversationId":1,"kind":"retained","data":{"retained":true}}`,
			"commit_seq": 1,
		})
		expectRow(t, database, "SELECT next_id, next_seq FROM durable_metadata WHERE singleton = 1",
			sqlite.SqliteRow{"next_id": "3", "next_seq": 2})
		mustDo(t, database.Close())
	})
}
