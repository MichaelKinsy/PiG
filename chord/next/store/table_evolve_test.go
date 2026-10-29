package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/store/table-evolve.test.ts: `evolveTable` (slice 6a §9): versions appended and
// materialized at declaration, the edits recorded before and after the data step, their checks, the editor rule
// shared with contracts, and the types along the chain.
//
// Designed out: every `expectTypeOf` and `@ts-expect-error` line (the editor's types track columns, key and version
// in the type system; Go's TableEditor is one concrete type, and the runtime check each of those lines guards is
// ported as the case itself: the refusals below run the same edits and expect the same messages).
//
// Go forms: edits chain on the editor, the first failing edit is kept and EvolveTable returns it; a `rows` patch is
// `func(Row) (any, error)`.

func evolveEditsV1(t *testing.T) *TableDefinition {
	t.Helper()
	return qDefine(t, "edits", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("id", Str()),
			Col("file", Str()),
			Col("at", Int()),
			Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))),
		},
		Key:     []string{"id"},
		Indexes: IndexRecords{{Name: "by_file", Columns: []string{"file", "at"}}},
	})
}

func noPatch(Row) (any, error) { return Row{}, nil }

func columnNames(definition *TableDefinition) []string {
	names := make([]string, len(definition.Columns))
	for i, column := range definition.Columns {
		names[i] = column.Name
	}
	return names
}

func evolveErr(previous *TableDefinition, edit func(e *TableEditor) *TableEditor) error {
	_, err := EvolveTable(previous, edit)
	return err
}

func TestEvolveVersions(t *testing.T) {
	EditsV1 := evolveEditsV1(t)

	t.Run("each evolveTable appends version + 1, materialized and fingerprinted, linked to the one before", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor { return e.Add("dir", Str(contracts.Default(""))) })
		V3 := qEvolve(t, V2, func(e *TableEditor) *TableEditor { return e.Index("by_dir", []string{"dir"}) })
		qEqual(t, []int{V2.Version, V3.Version}, []int{2, 3})
		if V3.Evolution.Previous != V2 || V2.Evolution.Previous != EditsV1 {
			t.Fatalf("the versions are not linked to the ones before")
		}
		fingerprints := map[string]bool{EditsV1.Fingerprint: true, V2.Fingerprint: true, V3.Fingerprint: true}
		qEqual(t, len(fingerprints), 3)
		qEqual(t, V3.Indexes, IndexRecords{
			{Name: "by_file", Columns: []string{"file", "at"}},
			{Name: "by_dir", Columns: []string{"dir"}},
		})
	})

	t.Run("the edit must return the editor it was given (contracts' rule)", func(t *testing.T) {
		err := evolveErr(EditsV1, func(*TableEditor) *TableEditor { return nil })
		qFails(t, err, "edits: evolveTable must return the editor it was given")
	})
}

func TestEvolveEdits(t *testing.T) {
	EditsV1 := evolveEditsV1(t)

	t.Run("are recorded before and after the data step; the step reads the columns as they stand there", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor {
			return e.
				Rename("file", "path").
				Add("dir", Str(contracts.Default(""))).
				Rows(func(row Row) (any, error) { return Row{"dir": row["path"]}, nil }).
				Drop("at").
				Index("by_dir", []string{"dir"})
		})
		migration := V2.Evolution.Migration
		qEqual(t, migration.Before, []TableEdit{
			{Kind: EditRename, From: "file", To: "path"},
			{Kind: EditAdd, Name: "dir", Column: ColumnRecord{Type: ColStr, Default: ""}},
		})
		qEqual(t, migration.After, []TableEdit{
			{Kind: EditDropIndex, Name: "by_file"},
			{Kind: EditDrop, Name: "at"},
			{Kind: EditIndex, Name: "by_dir", Columns: []string{"dir"}},
		})
		step := migration.Step
		qEqual(t, step.Kind, StepRows)
		qEqual(t, columnNames(step.Table), []string{"id", "path", "at", "meta", "dir"})
		qEqual(t, columnNames(V2), []string{"id", "path", "meta", "dir"})
		qEqual(t, V2.Indexes, IndexRecords{{Name: "by_dir", Columns: []string{"dir"}}})
	})

	t.Run("a rename carries the key and the indexes", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("id", "uid").Rename("file", "path") })
		qEqual(t, V2.Key, []string{"uid"})
		qEqual(t, V2.Indexes, IndexRecords{{Name: "by_file", Columns: []string{"path", "at"}}})
		qEqual(t, columnNames(V2), []string{"uid", "path", "at", "meta"})
	})

	t.Run("an update step is compiled at declaration against the columns there", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor {
			return e.Update(UpdateStep{
				Where: func(r ColumnRefs) Condition { return Eq(r["file"], "x") },
				Set:   func(r ColumnRefs) SetOf { return SetOf{Assign("at", Add(r["at"], 1))} },
			})
		})
		step := V2.Evolution.Migration.Step
		qEqual(t, step.Kind, StepUpdate)
		qEqual(t, *step.Where, whereCmp(WhereEq, "file", "x"))
		qEqual(t, step.Set, SetNode{{Column: "at", Node: ExprNode{Op: ExprAdd, Args: []ExprNode{colNode("at"), litNode(int64(1))}}}})
		err := evolveErr(EditsV1, func(e *TableEditor) *TableEditor {
			return e.Update(UpdateStep{Set: SetOf{Assign("id", "x")}})
		})
		qFails(t, err, `key column "id" cannot change`)
	})

	t.Run("a json edit changes the column's version and keeps its nullability", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor {
			return e.Json("meta", Json(
				contracts.Obj(contracts.Prop("a", contracts.Int()), contracts.Prop("b", contracts.Int())),
				JsonColumnOptions{Version: 2, Migrate: func(old delta.JsonValue, from int) (any, error) {
					return delta.JsonObjectOf("a", old.(*delta.JsonObject).Value("a"), "b", float64(0)), nil
				}},
			))
		})
		meta := V2.ByName["meta"]
		qEqual(t, []any{meta.Type, meta.Optional}, []any{ColJson, true})
		before := V2.Evolution.Migration.Before
		qEqual(t, len(before), 1)
		qEqual(t, []any{before[0].Kind, before[0].Name}, []any{EditJSON, "meta"})
		column := before[0].Column
		qEqual(t, []any{column.Type, column.Optional, *column.Version}, []any{ColJson, true, 2})
	})
}

func TestEvolveChecks(t *testing.T) {
	EditsV1 := evolveEditsV1(t)

	t.Run("a rename may change only the case of its own column", func(t *testing.T) {
		V2 := qEvolve(t, EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("at", "AT") })
		if _, found := V2.ByName["AT"]; !found {
			t.Fatalf("no column AT")
		}
	})

	jsonV2 := func(fields ...contracts.Property) ColumnSchema {
		return Json(contracts.Obj(fields...), JsonColumnOptions{Version: 2})
	}
	for _, c := range []struct {
		name    string
		declare func() error
		message string
	}{
		{"rename to an existing column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("file", "at") })
		}, `column "at" exists`},
		{"rename an unknown column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("nope", "x") })
		}, `no column "nope"`},
		{"add a column without opt(...) or a default", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Add("dir", Str()) })
		}, "needs opt(...) or a default"},
		{"drop a key column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Drop("id") })
		}, `"id" is a key column`},
		{"rename after a drop", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Drop("at").Rename("file", "path") })
		}, "rename comes before the data step"},
		{"a second data step", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Rows(noPatch).Rows(noPatch) })
		}, "rows comes before the data step"},
		{"an existing index", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Index("by_file", []string{"at"}) })
		}, `index "by_file" exists`},
		{"dropIndex of an unknown index", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.DropIndex("nope") })
		}, `no index "nope"`},
		{"an index over an opt(...) column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Index("by_meta", []string{"meta"}) })
		}, "is not a non-opt str() or int() column"},
		{"a json edit of a scalar column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Json("file", jsonV2()) })
		}, "is not a json(...) column"},
		{"a json edit changing the schema at the cell's version (D9)", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor {
				return e.Json("meta", Json(contracts.Obj(contracts.Prop("b", contracts.Str())), JsonColumnOptions{Version: 1}))
			})
		}, "changes its schema at version 1"},
		{"a json edit going back a version", func() error {
			v2, err := EvolveTable(EditsV1, func(e *TableEditor) *TableEditor {
				return e.Json("meta", jsonV2(contracts.Prop("a", contracts.Int())))
			})
			if err != nil {
				return err
			}
			return evolveErr(v2, func(e *TableEditor) *TableEditor {
				return e.Json("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))
			})
		}, "goes back from version 2"},
		{"an add colliding by case with a column a later drop removes", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor {
				return e.Add("File", Str(contracts.Default(""))).Drop("file")
			})
		}, `column "file" exists`},
		{"a rename colliding by case with another column", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("at", "FILE") })
		}, `column "file" exists`},
		{"a name folding to a column's only beyond ASCII (SQLite folds ASCII): the name rule, not a collision", func() error {
			kinds := qDefine(t, "kinds", TableOptions{
				Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("kind", Str())}, Key: []string{"id"},
			})
			return evolveErr(kinds, func(e *TableEditor) *TableEditor { return e.Add("\u212Aind", Opt(Str())) })
		}, "expected [A-Za-z_][A-Za-z0-9_]*"},
		{"an index on a column missing at that edit, though a later dropIndex removes the index", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Index("bad", []string{"missing"}).DropIndex("bad") })
		}, `index "bad": no column "missing"`},
		{"an index on a column dropped before it", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Drop("at").Index("by_at", []string{"at"}) })
		}, `index "by_at": no column "at"`},
		{"a rename to a name breaking the column name rule, though a later rename fixes it", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor {
				return e.Rename("at", "bad\x00name").Rename("bad\x00name", "when")
			})
		}, "rename: column \"bad\x00name\": expected [A-Za-z_][A-Za-z0-9_]*"},
		{"a rename to a name breaking the column name rule, though a later drop removes it", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Rename("at", "1st").Drop("1st") })
		}, `rename: column "1st": expected`},
		{"an index name outside [a-z0-9_]+", func() error {
			return evolveErr(EditsV1, func(e *TableEditor) *TableEditor { return e.Index("By", []string{"file"}) })
		}, "expected [a-z0-9_]+"},
	} {
		t.Run("refuses "+c.name+" at declaration", func(t *testing.T) {
			qFails(t, c.declare(), c.message)
		})
	}
}
