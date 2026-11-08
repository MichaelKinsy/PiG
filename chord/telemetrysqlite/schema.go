package telemetrysqlite

// Ports packages/chord/src/telemetry-sqlite/schema.ts (Pi main c23b9ec1). The SQLite telemetry store's tables and their
// versions: the migrations a recorder applies, and the check a reader makes that a file holds a store of this version.

import (
	"errors"
	"fmt"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// versionTable is the table that records the store's version.
const versionTable = "telemetry_schema"

// migrations are the store's versions. 1: spans with their start and end in milliseconds (`started_ms`, `ended_ms`,
// whole milliseconds at the start); 2: in whole microseconds (`started_us`, `ended_us`, integers), the rows of 1 kept,
// their times multiplied.
var migrations = []chordsqlite.SqliteMigration{
	{
		Version: 1,
		Statements: []string{
			`CREATE TABLE telemetry_spans (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, parent_id TEXT, name TEXT, started_ms REAL,
			ended_ms REAL NOT NULL, attributes TEXT, events TEXT, links TEXT, status TEXT)`,
			"CREATE INDEX telemetry_spans_ended_ms ON telemetry_spans(ended_ms)",
			"CREATE INDEX telemetry_spans_trace_started ON telemetry_spans(trace_id, started_ms)",
			"CREATE INDEX telemetry_spans_started_ms ON telemetry_spans(started_ms)",
			`CREATE TABLE telemetry_metrics (interval_ms INTEGER, series_key TEXT, name TEXT, kind TEXT,
			unit TEXT, attributes TEXT, count INTEGER, sum REAL, min REAL, max REAL, PRIMARY KEY(interval_ms, series_key))`,
		},
	},
	{
		Version: 2,
		Statements: []string{
			`CREATE TABLE telemetry_spans_us (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, parent_id TEXT, name TEXT,
			started_us INTEGER, ended_us INTEGER NOT NULL, attributes TEXT, events TEXT, links TEXT, status TEXT)`,
			`INSERT INTO telemetry_spans_us SELECT id, trace_id, parent_id, name, CAST(round(started_ms * 1000) AS INTEGER),
			CAST(round(ended_ms * 1000) AS INTEGER), attributes, events, links, status FROM telemetry_spans`,
			"DROP TABLE telemetry_spans",
			"ALTER TABLE telemetry_spans_us RENAME TO telemetry_spans",
			"CREATE INDEX telemetry_spans_ended_us ON telemetry_spans(ended_us)",
			"CREATE INDEX telemetry_spans_trace_started ON telemetry_spans(trace_id, started_us)",
			"CREATE INDEX telemetry_spans_started_us ON telemetry_spans(started_us)",
		},
	},
}

// migrateTelemetryStore brings the store in db to this version (made when absent; one of an older version migrated, its
// rows kept).
func migrateTelemetryStore(db chordsqlite.SqliteDatabase) error {
	return chordsqlite.ApplySqliteMigrations(db, migrations, chordsqlite.ApplySqliteMigrationsOptions{Table: versionTable})
}

// assertTelemetryStore fails unless db holds a store of this version: a reader cannot migrate (a recorder of this
// version, opened on the file once, does).
func assertTelemetryStore(db chordsqlite.SqliteDatabase) error {
	expected := migrations[len(migrations)-1].Version
	// The file is the trust boundary here: any file may be named, a store or not.
	tables, err := db.All("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", versionTable)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return errors.New("not a telemetry store: no telemetry_schema table")
	}
	row, err := db.Get("SELECT version FROM " + versionTable + " WHERE singleton = 1")
	if err != nil {
		return err
	}
	// Pi compares the stored number: no integer conversion.
	version := float64(0)
	if row != nil {
		number, err := rowNumber(row, "version")
		if err != nil {
			return err
		}
		version = number
	}
	if version > float64(expected) {
		return fmt.Errorf("telemetry store version %v is newer than this reader's %d", version, expected)
	}
	if version < float64(expected) {
		return fmt.Errorf("telemetry store version %v, not %d: a recorder (CreateSqliteTelemetry) migrates it", version, expected)
	}
	return nil
}
