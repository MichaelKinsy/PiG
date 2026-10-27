// Package sqlitetest holds the helpers the Chord SQLite driver tests share.
package sqlitetest

// Ports packages/chord/test/sqlite/helpers.ts

import (
	"fmt"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// RunChanges returns the rows each kind of statement changes, as RunCounted reports them: on a table with two indexes,
// so a driver that counted index writes (a Durable Object cursor's rows written) would report more.
func RunChanges(database chordsqlite.SqliteExecutor) (map[string]int64, error) {
	for _, sql := range []string{
		"CREATE TABLE changes_probe (id TEXT PRIMARY KEY, path TEXT NOT NULL, n INTEGER NOT NULL) STRICT, WITHOUT ROWID",
		"CREATE INDEX changes_probe_path ON changes_probe (path, id)",
		"CREATE INDEX changes_probe_n ON changes_probe (n, id)",
	} {
		if err := database.Exec(sql); err != nil {
			return nil, err
		}
	}
	for i := range 10 {
		if err := database.Run("INSERT INTO changes_probe VALUES (?, ?, ?)", fmt.Sprintf("k%d", i), fmt.Sprintf("a/%d", i), i); err != nil {
			return nil, err
		}
	}
	steps := []struct {
		name   string
		sql    string
		params []chordsqlite.SqliteValue
	}{
		{"insert", "INSERT INTO changes_probe VALUES (?, ?, ?)", []chordsqlite.SqliteValue{"x", "a/x", 99}},
		{"update", "UPDATE changes_probe SET path = ? WHERE id = ?", []chordsqlite.SqliteValue{"b/x", "x"}},
		{"none", "UPDATE changes_probe SET n = 0 WHERE id = ?", []chordsqlite.SqliteValue{"absent"}},
		{"bulk", "UPDATE changes_probe SET path = 'c/' || substr(path, 3) WHERE path >= 'a/' AND path < 'a0'", nil},
		{"upsert", "INSERT INTO changes_probe VALUES (?, ?, ?) ON CONFLICT (id) DO UPDATE SET n = excluded.n", []chordsqlite.SqliteValue{"x", "b/x", 5}},
		{"delete", "DELETE FROM changes_probe WHERE id = ?", []chordsqlite.SqliteValue{"x"}},
		{"bulkDelete", "DELETE FROM changes_probe WHERE path >= 'c/' AND path < 'c0'", nil},
	}
	changes := make(map[string]int64, len(steps))
	for _, step := range steps {
		changed, err := database.RunCounted(step.sql, step.params...)
		if err != nil {
			return nil, err
		}
		changes[step.name] = changed
	}
	return changes, nil
}

// RunChangesExpected is what RunChanges reports on every driver.
var RunChangesExpected = map[string]int64{
	"insert": 1, "update": 1, "none": 0, "bulk": 10, "upsert": 1, "delete": 1, "bulkDelete": 10,
}
