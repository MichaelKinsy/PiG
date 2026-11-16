package store

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/store/column-set.test.ts: `defineColumns` (slice 14 §5): what a declaration keeps and
// refuses, `exposes` on a table (checked, no part of the fingerprint), and `evolveTable` carrying the set over.
//
// Go forced differences: a set's columns are an ordered slice of ColumnProp (Schemas) and its indexes IndexRecords, as
// a TableDefinition's are; `evolveTable`'s `exposes: "none"` is EvolveOptions.ExposesNone.

func csSession(t testing.TB) *contracts.HostKind {
	t.Helper()
	kind, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
	if err != nil {
		t.Fatalf("NewHostKind: %v", err)
	}
	return kind
}

func csColumns(t testing.TB) *ColumnSet {
	t.Helper()
	set, err := DefineColumns("sessions.columns", ColumnSetOptions{
		Columns: []ColumnProp{
			Col("id", Host(csSession(t))), Col("app", Str()), Col("private", Bool()), Col("archivedAt", Opt(Int())),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "ofApp", Columns: []string{"app"}}},
	})
	if err != nil {
		t.Fatalf("DefineColumns: %v", err)
	}
	return set
}

// csWith replaces the schema of the column name, or appends it, as an object spread then an override does.
func csWith(props []ColumnProp, extra ...ColumnProp) []ColumnProp {
	out := slices.Clone(props)
	for _, prop := range extra {
		at := slices.IndexFunc(out, func(own ColumnProp) bool { return own.Name == prop.Name })
		if at < 0 {
			out = append(out, prop)
		} else {
			out[at] = prop
		}
	}
	return out
}

func TestColumnSetDefine(t *testing.T) {
	Columns := csColumns(t)

	t.Run("keeps its columns, key and indexes, and hands out references tagged with its name", func(t *testing.T) {
		names := make([]string, len(Columns.Columns))
		for i, column := range Columns.Columns {
			names[i] = column.Name
		}
		qEqual(t, names, []string{"id", "app", "private", "archivedAt"})
		qEqual(t, Columns.Key, []string{"id"})
		qEqual(t, Columns.Indexes, IndexRecords{{Name: "ofApp", Columns: []string{"app"}}})
		column, _ := Columns.Refs["app"].isRef()
		qEqual(t, []string{column, Columns.Refs["app"].Set}, []string{"app", "sessions.columns"})
	})

	t.Run("refuses a json column, a key or an index of a column it has not, and a bad name", func(t *testing.T) {
		meta := Json(contracts.Obj(), JsonColumnOptions{Version: 1})
		_, err := DefineColumns("x", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str()), Col("meta", meta)}, Key: []string{"id"}})
		qFails(t, err, `column "meta": json(...) is not in a column set`)
		_, err = DefineColumns("x", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str())}, Key: []string{"nope"}})
		qFails(t, err, `no column "nope"`)
		_, err = DefineColumns("x", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"},
			Indexes: IndexRecords{{Name: "a", Columns: []string{"b"}}},
		})
		qFails(t, err, `index "a": no column "b"`)
		_, err = DefineColumns("Bad Name", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"}})
		qFails(t, err, "expected lowercase")
	})
}

func TestColumnSetExposes(t *testing.T) {
	Columns := csColumns(t)
	table := func(columns []ColumnProp, key []string, indexes IndexRecords) (*TableDefinition, error) {
		return DefineTable("rows", TableOptions{Version: 1, Columns: columns, Key: key, Indexes: indexes, Exposes: Columns})
	}
	byApp := IndexRecords{{Name: "by_app", Columns: []string{"app", "id"}}}

	t.Run("takes a table that spreads the set's columns, with the key and an index path per declared index", func(t *testing.T) {
		Rows, err := table(csWith(Columns.Schemas, Col("cwd", Str())), []string{"id"}, byApp)
		if err != nil {
			t.Fatal(err)
		}
		if Rows.Exposes != Columns {
			t.Fatalf("Exposes = %p, want the set %p", Rows.Exposes, Columns)
		}
	})

	t.Run("is no part of the table's fingerprint", func(t *testing.T) {
		options := TableOptions{
			Version: 1, Columns: slices.Clone(Columns.Schemas), Key: []string{"id"},
			Indexes: IndexRecords{{Name: "by_app", Columns: []string{"app"}}},
		}
		with := options
		with.Exposes = Columns
		qEqual(t, qDefine(t, "rows", with).Fingerprint, qDefine(t, "rows", options).Fingerprint)
	})

	t.Run("refuses a missing column, another type, another key and an index the table lacks", func(t *testing.T) {
		withoutApp := slices.DeleteFunc(slices.Clone(Columns.Schemas), func(p ColumnProp) bool { return p.Name == "app" })
		_, err := table(append(withoutApp, Col("cwd", Str())), []string{"id"}, IndexRecords{})
		qFails(t, err, `the table has no column "app"`)
		_, err = table(csWith(Columns.Schemas, Col("app", Int())), []string{"id"}, byApp)
		qFails(t, err, `column "app" is declared differently in the table`)
		_, err = table(csWith(Columns.Schemas, Col("n", Int())), []string{"n"}, byApp)
		qFails(t, err, "the keys differ")
		_, err = table(slices.Clone(Columns.Schemas), []string{"id"}, IndexRecords{})
		qFails(t, err, `index "ofApp" is no leading prefix of the key or of an index of the table`)
	})
}

func TestColumnSetEvolveExposes(t *testing.T) {
	Columns := csColumns(t)
	Rows := qDefine(t, "rows", TableOptions{
		Version: 1, Columns: slices.Clone(Columns.Schemas), Key: []string{"id"},
		Indexes: IndexRecords{{Name: "by_app", Columns: []string{"app"}}}, Exposes: Columns,
	})
	evolve := func(edit func(e *TableEditor) TableTailEditor, options ...EvolveOptions) *TableDefinition {
		t.Helper()
		definition, err := EvolveTable(Rows, edit, options...)
		if err != nil {
			t.Fatal(err)
		}
		return definition
	}

	t.Run("carries the set over, replaces it, or drops it", func(t *testing.T) {
		addCwd := func(e *TableEditor) TableTailEditor { return e.Add("cwd", Opt(Str())) }
		if evolve(addCwd).Exposes != Columns {
			t.Fatal("the next version does not carry the set over")
		}
		if evolve(addCwd, EvolveOptions{ExposesNone: true}).Exposes != nil {
			t.Fatal(`exposes "none" does not drop the set`)
		}
		Narrow, err := DefineColumns("sessions.columns", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Host(csSession(t))), Col("app", Str())}, Key: []string{"id"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if evolve(func(e *TableEditor) TableTailEditor { return e.Drop("private") }, EvolveOptions{Exposes: Narrow}).Exposes != Narrow {
			t.Fatal("the new set is not the one exposed")
		}
	})

	t.Run("refuses a version that no longer has a column of the set it exposes", func(t *testing.T) {
		_, err := EvolveTable(Rows, func(e *TableEditor) TableTailEditor { return e.Drop("private") })
		qFails(t, err, `the table has no column "private"`)
	})
}
