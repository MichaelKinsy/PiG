package sqlite

// Ports packages/chord/src/sqlite/migrations.ts

import (
	"errors"
	"fmt"
)

// SqliteMigration is one immutable schema version and the statements that create it from the previous version.
type SqliteMigration struct {
	Version    int
	Statements []string
}

// ApplySqliteMigrationsOptions names the metadata table the applied version is tracked in.
type ApplySqliteMigrationsOptions struct {
	Table string
}

// ApplySqliteMigrations applies every pending migration atomically, tracking the applied version in the metadata table
// options.Table names. The table name must pass ValidateSqliteSchemaPrefix and the versions must run 1, 2, 3...; both
// are checked before the database is touched.
func ApplySqliteMigrations(database SqliteDatabase, migrations []SqliteMigration, options ApplySqliteMigrationsOptions) error {
	table := options.Table
	if err := ValidateSqliteSchemaPrefix(table); err != nil {
		return err
	}
	for index, migration := range migrations {
		if migration.Version != index+1 {
			return errors.New("SQLite migrations must have contiguous versions starting at 1")
		}
	}
	return database.Transaction(func(transaction SqliteExecutor) error {
		if err := transaction.Exec(`CREATE TABLE IF NOT EXISTS ` + table + ` (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			version INTEGER NOT NULL CHECK (version >= 0)
		) STRICT`); err != nil {
			return err
		}
		if err := transaction.Run("INSERT OR IGNORE INTO " + table + " (singleton, version) VALUES (1, 0)"); err != nil {
			return err
		}
		row, err := transaction.Get("SELECT version FROM " + table + " WHERE singleton = 1")
		if err != nil {
			return err
		}
		if row == nil {
			return errors.New("SQLite schema metadata is missing")
		}
		version, err := RowInt(row, "version")
		if err != nil {
			return err
		}
		currentVersion := 0
		if len(migrations) > 0 {
			currentVersion = migrations[len(migrations)-1].Version
		}
		if version > int64(currentVersion) {
			return fmt.Errorf("SQLite schema version %d is newer than supported version %d", version, currentVersion)
		}
		for _, migration := range migrations {
			if int64(migration.Version) <= version {
				continue
			}
			for _, statement := range migration.Statements {
				if err := transaction.Exec(statement); err != nil {
					return err
				}
			}
			if err := transaction.Run("UPDATE "+table+" SET version = ? WHERE singleton = 1", migration.Version); err != nil {
				return err
			}
		}
		return nil
	})
}

// RowInt reads an INTEGER column of a row.
func RowInt(row SqliteRow, column string) (int64, error) {
	switch value := row[column].(type) {
	case int64:
		return value, nil
	case int:
		return int64(value), nil
	case float64:
		// A driver returns a whole REAL as float64; only a value that round-trips through int64 is read, so NaN, the
		// infinities and out-of-range values fall through to the error, where Pi's `row.version > currentVersion` would
		// compare them as numbers.
		if value == float64(int64(value)) { //portlint:allow numbers guarded: converts only a value that round-trips through int64
			return int64(value), nil //portlint:allow numbers guarded by the round-trip check above
		}
	}
	return 0, fmt.Errorf("SQLite column %s is not an integer: %v", column, row[column])
}
