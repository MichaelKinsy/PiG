package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/store/table.test.ts: `defineTable` (slice 6a §5): what a declaration materializes,
// the checks it makes, and its fingerprint.
//
// Also the helpers of the query lane's tests (table, expression, evaluate, query, history, evolve and migration
// tests): qDefine, qEvolve, qEqual, qFails, qJSON.
//
// Designed out: nothing (every case is ported; the type-level `@ts-expect-error` and `expectTypeOf` lines of the
// other files are noted there).

// qDefine is DefineTable, failing the test on its error.
func qDefine(t testing.TB, name string, options TableOptions) *TableDefinition {
	t.Helper()
	definition, err := DefineTable(name, options)
	if err != nil {
		t.Fatalf("DefineTable(%s): %v", name, err)
	}
	return definition
}

// qEvolve is EvolveTable, failing the test on its error.
func qEvolve(t testing.TB, previous *TableDefinition, edit func(e *TableEditor) *TableEditor) *TableDefinition {
	t.Helper()
	definition, err := EvolveTable(previous, edit)
	if err != nil {
		t.Fatalf("EvolveTable(%s): %v", previous.Name, err)
	}
	return definition
}

// qEqual fails the test unless got and want are deeply equal.
func qEqual(t testing.TB, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// qFails fails the test unless err is non-nil and its message contains want.
func qFails(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error containing %q, got none", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

// qJSON is JSON.stringify(value).
func qJSON(t testing.TB, value any) string {
	t.Helper()
	text, err := jsstring.MarshalJSON(value)
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	return string(text)
}

func tableColumns() []ColumnProp {
	return []ColumnProp{
		Col("id", Str()),
		Col("path", Str()),
		Col("at", Int()),
		Col("note", Opt(Str())),
		Col("meta", Json(contracts.Obj(), JsonColumnOptions{Version: 1})),
	}
}

func TestDefineTable(t *testing.T) {
	t.Run("materializes columns, key, indexes and the record", func(t *testing.T) {
		edits := qDefine(t, "edits", TableOptions{
			Version: 2, Columns: tableColumns(), Key: []string{"id"},
			Indexes: IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}},
		})
		qEqual(t, edits.Name, "edits")
		qEqual(t, edits.Version, 2)
		qEqual(t, edits.Key, []string{"id"})
		qEqual(t, edits.Indexes, IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}})
		qEqual(t, edits.Record.Names(), []string{"id", "path", "at", "note", "meta"})
		qEqual(t, edits.Rows.Physical, []string{"id", "path", "at", "note", "meta", "meta$v", "$v"})
	})

	t.Run("checks the table name, the version, key, index names and index columns", func(t *testing.T) {
		define := func(name string, version int, key []string, indexes IndexRecords) (*TableDefinition, error) {
			return DefineTable(name, TableOptions{Version: version, Columns: tableColumns(), Key: key, Indexes: indexes})
		}
		id := []string{"id"}
		_, err := define("Edits", 1, id, nil)
		qFails(t, err, "expected lowercase")
		_, err = define("a:b", 1, id, nil)
		qFails(t, err, "expected lowercase")
		scoped, err := define("@pi/file-history.edits", 1, id, nil)
		if err != nil {
			t.Fatalf("DefineTable: %v", err)
		}
		qEqual(t, scoped.Name, "@pi/file-history.edits")
		_, err = define("edits", 0, id, nil)
		qFails(t, err, "positive integer")
		_, err = define("edits", 1, []string{}, nil)
		qFails(t, err, "no column")
		_, err = define("edits", 1, []string{"note"}, nil)
		qFails(t, err, "non-opt str() or int()")
		_, err = define("edits", 1, []string{"meta"}, nil)
		qFails(t, err, "non-opt str() or int()")
		_, err = define("edits", 1, []string{"id", "id"}, nil)
		qFails(t, err, "occurs twice")
		_, err = define("edits", 1, []string{"nope"}, nil)
		qFails(t, err, `no column "nope"`)
		_, err = define("edits", 1, id, IndexRecords{{Name: "ByPath", Columns: []string{"path"}}})
		qFails(t, err, "[a-z0-9_]+")
		_, err = define("edits", 1, id, IndexRecords{{Name: "by", Columns: []string{"note"}}})
		qFails(t, err, "non-opt")
	})

	t.Run("fingerprints columns, key and indexes, not their declaration order", func(t *testing.T) {
		columns := tableColumns()
		indexes := IndexRecords{{Name: "x", Columns: []string{"path"}}, {Name: "y", Columns: []string{"at"}}}
		a := qDefine(t, "edits", TableOptions{Version: 1, Columns: columns, Key: []string{"id"}, Indexes: indexes})
		reordered := []ColumnProp{columns[4], columns[3], columns[2], columns[1], columns[0]}
		b := qDefine(t, "edits", TableOptions{
			Version: 1, Columns: reordered, Key: []string{"id"},
			Indexes: IndexRecords{indexes[1], indexes[0]},
		})
		qEqual(t, b.Fingerprint, a.Fingerprint)
		bare := qDefine(t, "edits", TableOptions{Version: 1, Columns: columns, Key: []string{"id"}})
		if bare.Fingerprint == a.Fingerprint {
			t.Fatalf("dropping the indexes keeps the fingerprint")
		}
		longKey := qDefine(t, "edits", TableOptions{Version: 1, Columns: columns, Key: []string{"id", "at"}, Indexes: indexes})
		if longKey.Fingerprint == a.Fingerprint {
			t.Fatalf("another key keeps the fingerprint")
		}
		otherMeta := append([]ColumnProp{}, columns...)
		otherMeta[4] = Col("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))
		changed := qDefine(t, "edits", TableOptions{Version: 1, Columns: otherMeta, Key: []string{"id"}, Indexes: indexes})
		if changed.Fingerprint == a.Fingerprint {
			t.Fatalf("another json schema keeps the fingerprint")
		}
	})
}
