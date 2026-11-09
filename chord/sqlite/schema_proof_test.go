package sqlite_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// Proof of packages/chord/src/sqlite/schema.ts validateSqliteSchemaPrefix: which prefixes it refuses and with what message
// (migrations.test.ts "validates prefixes" checks only that they fail).

func TestValidateSqliteSchemaPrefixNamesWhyAPrefixIsRefused(t *testing.T) {
	for prefix, want := range map[string]string{
		"":           `Invalid SQLite schema prefix ""`,
		"1bad":       `Invalid SQLite schema prefix "1bad"`,
		"bad-prefix": `Invalid SQLite schema prefix "bad-prefix"`,
		"a\"b":       `Invalid SQLite schema prefix "a\"b"`,
		"sqlite_app": `SQLite schema prefix "sqlite_app" is reserved`,
		"SQLite_App": `SQLite schema prefix "SQLite_App" is reserved`,
		"SQLITE_":    `SQLite schema prefix "SQLITE_" is reserved`,
		"sqlite_":    `SQLite schema prefix "sqlite_" is reserved`,
	} {
		err := sqlite.ValidateSqliteSchemaPrefix(prefix)
		if err == nil || err.Error() != want {
			t.Fatalf("ValidateSqliteSchemaPrefix(%q) = %v, want %q", prefix, err, want)
		}
	}
	for _, prefix := range []string{"a", "_", "sqlite", "sqliteapp", "my_sqlite_x", "A9_z"} {
		if err := sqlite.ValidateSqliteSchemaPrefix(prefix); err != nil {
			t.Fatalf("ValidateSqliteSchemaPrefix(%q) = %v, want nil", prefix, err)
		}
	}
}
