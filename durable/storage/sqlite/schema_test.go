package sqlite_test

// pi: packages/durable/src/storage/sqlite/schema.ts

// Ports the identifiers of packages/durable/src/storage/sqlite/schema.ts: TABLE_NAMES (schema.ts:8-18), INDEX_NAMES
// (schema.ts:20-37) and createSqliteSchema's prefix handling (schema.ts:49-57). The names are immutable history, so the
// literals here are Pi's.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

var piTables = []string{
	"durable_schema", "durable_metadata", "record_ids", "conversations", "entries", "tasks", "submissions", "documents",
	"document_revisions",
}

var piIndexes = []string{
	"conversations_by_owner_conversation", "conversations_by_owner_task", "entries_by_conversation",
	"entry_heads_by_conversation", "tasks_by_status", "tasks_by_conversation", "tasks_by_kind", "tasks_by_abort_requested",
	"tasks_by_background", "submissions_by_request", "submissions_by_conversation", "submissions_by_status",
	"documents_by_address", "documents_by_scope", "documents_by_scope_kind", "document_revisions_by_kind",
}

func values(t *testing.T, value any) []string {
	t.Helper()
	v := reflect.ValueOf(value)
	out := make([]string, v.NumField())
	for i := range out {
		out[i] = v.Field(i).String()
	}
	return out
}

func prefixed(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = prefix + name
	}
	return out
}

func TestSqliteSchemaNamesAreUpstreamsImmutableIdentifiers(t *testing.T) {
	schema := sqlite.DefaultSqliteSchema
	if got := values(t, schema.Tables); !reflect.DeepEqual(got, piTables) {
		t.Errorf("tables %v, want %v", got, piTables)
	}
	if got := values(t, schema.Indexes); !reflect.DeepEqual(got, piIndexes) {
		t.Errorf("indexes %v, want %v", got, piIndexes)
	}
}

func TestCreateSqliteSchemaPrefixesEveryIdentifierAndValidatesThePrefix(t *testing.T) {
	prefix := "app_"
	schema, err := sqlite.CreateSqliteSchema(&prefix)
	if err != nil {
		t.Fatal(err)
	}
	if got := values(t, schema.Tables); !reflect.DeepEqual(got, prefixed(prefix, piTables)) {
		t.Errorf("tables %v", got)
	}
	if got := values(t, schema.Indexes); !reflect.DeepEqual(got, prefixed(prefix, piIndexes)) {
		t.Errorf("indexes %v", got)
	}
	empty := ""
	if schema, err := sqlite.CreateSqliteSchema(&empty); err == nil {
		t.Errorf("an empty prefix is present, so it must pass validation: %+v", schema)
	}
	for prefix, message := range map[string]string{
		"1bad":    `Invalid SQLite schema prefix "1bad"`,
		"SQLite_": `SQLite schema prefix "SQLite_" is reserved`,
	} {
		if _, err := sqlite.CreateSqliteSchema(&prefix); err == nil || err.Error() != message {
			t.Errorf("prefix %q: %v, want %q", prefix, err, message)
		}
	}
}
