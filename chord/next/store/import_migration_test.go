package store

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/store/import-migration.test.ts: tests of rows of an imported table brought to the recorded
// version (spec `docs/host-keyed/spec.md` §4): edits and data steps (`rows`, `update`) in order, `$v` and `json(...)`
// cell versions exact, an archive exported during a migration resumed, and a version outside the owner's history
// refused.
//
// Go forms: Pi's `toEqual` on rows compares structurally (key order does not matter), so the expected rows are
// compared as decoded JSON (importMigrationSame); a row's cells are the *delta.JsonObject of the `put`.
//
// pi: packages/chord/src/store/import-migration.ts

const importMigrationTable = "app.s:labels"

// importMigrationDefinitions are the tables of the cases: V1, its evolution V2 (a rename, an added column, a `rows`
// step) and V3 (an `update` step, a dropped column).
func importMigrationDefinitions(t *testing.T) (v1, v2, v3 *TableDefinition) {
	t.Helper()
	session, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
	must(t, err)
	v1 = qDefine(t, "labels", TableOptions{
		Version: 1,
		Columns: []ColumnProp{
			Col("session", Host(session)), Col("label", Str()), Col("n", Int()),
			Col("meta", Opt(Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1}))),
		},
		Key: []string{"session", "label"},
	})
	v2 = qEvolve(t, v1, func(e *TableEditor) TableTailEditor {
		return e.Rename("n", "count").Add("color", Str(contracts.Default("red"))).Rows(func(row Row) (any, error) {
			return Row{"count": row["count"].(int64) * 2}, nil
		})
	})
	v3 = qEvolve(t, v2, func(e *TableEditor) TableTailEditor {
		return e.Update(UpdateStep{
			Where: func(row ColumnRefs) Condition { return Eq(row["label"], "a") },
			Set:   map[string]any{"color": "blue"},
		}).Drop("meta")
	})
	return v1, v2, v3
}

// importMigrationRecorded is definition as the store records it, state given.
func importMigrationRecorded(definition *TableDefinition) *TableRecord {
	return &TableRecord{
		Table: importMigrationTable, Version: definition.Version, State: StateReady, Columns: definition.Record,
		Key: definition.Key, Fingerprint: definition.Fingerprint, Indexes: definition.Indexes,
	}
}

// importMigrationWrite is the `table` write of definition (with the columns of layout, for a `migrating` one).
func importMigrationWrite(definition *TableDefinition, state TableState, layout *TableDefinition) *TableDefinitionWrite {
	return &TableDefinitionWrite{
		Table: importMigrationTable, Version: definition.Version, State: state, Columns: layout.Record, Key: layout.Key,
		Fingerprint: definition.Fingerprint,
	}
}

// importMigrationPut is a `put` of S1's row of pairs at `$v` version, with the json cell versions columns.
func importMigrationPut(version int, columns *delta.JsonObject, pairs ...any) *PutWrite {
	row := delta.JsonObjectOf("session", "session|S1")
	for index := 0; index < len(pairs); index += 2 {
		row.Set(pairs[index].(string), pairs[index+1])
	}
	return &PutWrite{Table: importMigrationTable, Row: row, Version: version, Columns: columns}
}

// importMigrationSame fails the test unless got, as JSON, is the JSON text want.
func importMigrationSame(t *testing.T, got any, want string) {
	t.Helper()
	var decoded, expected any
	must(t, json.Unmarshal([]byte(nexttest.JSONText(t, got)), &decoded))
	must(t, json.Unmarshal([]byte(want), &expected))
	qEqual(t, decoded, expected)
}

func TestMigratedRows(t *testing.T) {
	v1, v2, v3 := importMigrationDefinitions(t)
	versioned := delta.JsonObjectOf

	t.Run("go through each version's edits and data step, with `$v` and json cell versions exact", func(t *testing.T) {
		rows, err := migrateRows(
			importMigrationWrite(v1, StateReady, v1),
			[]*PutWrite{
				importMigrationPut(1, versioned("meta", float64(1)), "label", "a", "n", float64(1), "meta", delta.JsonObjectOf("a", float64(1))),
				importMigrationPut(1, versioned(), "label", "b", "n", float64(2)),
			},
			v3, importMigrationRecorded(v3), background,
		)
		must(t, err)
		qEqual(t, len(rows), 2)
		importMigrationSame(t, rows[0].Row, `{"session":"session|S1","label":"a","count":2,"color":"blue"}`)
		importMigrationSame(t, rows[0].Columns, `{}`)
		qEqual(t, rows[0].Version, 3)
		importMigrationSame(t, rows[1].Row, `{"session":"session|S1","label":"b","count":4,"color":"red"}`)
		importMigrationSame(t, rows[1].Columns, `{}`)
		qEqual(t, rows[1].Version, 2)
	})

	t.Run("stop at the version the store records", func(t *testing.T) {
		rows, err := migrateRows(
			importMigrationWrite(v1, StateReady, v1),
			[]*PutWrite{importMigrationPut(1, versioned("meta", float64(1)), "label", "a", "n", float64(1), "meta", delta.JsonObjectOf("a", float64(1)))},
			v3, importMigrationRecorded(v2), background,
		)
		must(t, err)
		importMigrationSame(t, rows[0].Row, `{"session":"session|S1","label":"a","count":2,"color":"red","meta":{"a":1}}`)
		importMigrationSame(t, rows[0].Columns, `{"meta":1}`)
		qEqual(t, rows[0].Version, 2)
	})

	t.Run("resume an archive exported during a migration: rows already stamped are not patched again", func(t *testing.T) {
		step := v2.Evolution.Migration.Step.Table
		rows, err := migrateRows(
			importMigrationWrite(v2, StateMigrating, step),
			[]*PutWrite{
				importMigrationPut(1, versioned(), "label", "a", "count", float64(1), "color", "red"),
				importMigrationPut(2, versioned(), "label", "b", "count", float64(10), "color", "red"),
			},
			v2, importMigrationRecorded(v2), background,
		)
		must(t, err)
		importMigrationSame(t, []any{rows[0].Row.Value("count"), rows[1].Row.Value("count")}, `[2,10]`)
	})

	t.Run("refuse a version the owner's history does not have", func(t *testing.T) {
		unknown := importMigrationWrite(v1, StateReady, v1)
		unknown.Version = 7
		_, err := migrateRows(unknown, nil, v3, importMigrationRecorded(v3), background)
		qFails(t, err, "is not in its owner's history")
	})

	t.Run("give a json(...) cell a data step sets the column's version then, and keep an untouched cell's", func(t *testing.T) {
		session, err := contracts.NewHostKind("session", contracts.Prop("id", contracts.Str()))
		must(t, err)
		w1 := qDefine(t, "notes", TableOptions{
			Version: 1,
			Columns: []ColumnProp{
				Col("session", Host(session)), Col("id", Str()),
				Col("meta", Json(contracts.Obj(contracts.Prop("a", contracts.Int())), JsonColumnOptions{Version: 1})),
			},
			Key: []string{"session", "id"},
		})
		w2 := qEvolve(t, w1, func(e *TableEditor) TableTailEditor {
			return e.Json("meta", Json(
				contracts.Obj(contracts.Prop("a", contracts.Int()), contracts.Prop("b", contracts.Int())),
				JsonColumnOptions{Version: 2, Migrate: func(old delta.JsonValue, _ int) (any, error) {
					return delta.JsonObjectOf("a", old.(*delta.JsonObject).Value("a"), "b", float64(0)), nil
				}},
			)).Rows(func(row Row) (any, error) {
				if row["id"] == "x" {
					return Row{"meta": delta.JsonObjectOf("a", row["meta"].(*delta.JsonObject).Value("a"), "b", float64(9))}, nil
				}
				return Row{}, nil
			})
		})
		write := &TableDefinitionWrite{Table: importMigrationTable, Version: 1, State: StateReady, Columns: w1.Record, Key: w1.Key, Fingerprint: w1.Fingerprint}
		rows, err := migrateRows(
			write,
			[]*PutWrite{
				importMigrationPut(1, versioned("meta", float64(1)), "id", "x", "meta", delta.JsonObjectOf("a", float64(1))),
				importMigrationPut(1, versioned("meta", float64(1)), "id", "y", "meta", delta.JsonObjectOf("a", float64(2))),
			},
			w2, importMigrationRecorded(w2), background,
		)
		must(t, err)
		importMigrationSame(t, rows[0].Row, `{"session":"session|S1","id":"x","meta":{"a":1,"b":9}}`)
		importMigrationSame(t, rows[0].Columns, `{"meta":2}`)
		qEqual(t, rows[0].Version, 2)
		importMigrationSame(t, rows[1].Row, `{"session":"session|S1","id":"y","meta":{"a":2}}`)
		importMigrationSame(t, rows[1].Columns, `{"meta":1}`)
		qEqual(t, rows[1].Version, 2)
	})
}
