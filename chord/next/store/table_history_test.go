package store

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports packages/chord/test/next/store/table-history.test.ts: a table's history (slice 6a §9): what an activation
// does with the recorded version: create, use, migrate from there (resuming a `migrating` one), or refuse a version
// the code cannot reach.

func historyVersions(t *testing.T) (v1, v2, v3 *TableDefinition) {
	t.Helper()
	v1 = qDefine(t, "edits", TableOptions{Version: 1, Columns: []ColumnProp{Col("id", Str()), Col("at", Int())}, Key: []string{"id"}})
	v2 = qEvolve(t, v1, func(e *TableEditor) *TableEditor {
		return e.Add("dir", Str(contracts.Default(""))).Rows(func(Row) (any, error) { return Row{}, nil })
	})
	v3 = qEvolve(t, v2, func(e *TableEditor) *TableEditor { return e.Index("by_at", []string{"at"}) })
	return v1, v2, v3
}

// recordedAs is the record of definition in state.
func recordedAs(definition *TableDefinition, state TableState) *TableRecord {
	return &TableRecord{
		Table: "t", Version: definition.Version, State: state, Columns: definition.Record, Key: definition.Key,
		Fingerprint: definition.Fingerprint, Indexes: IndexRecords{},
	}
}

func TestPlans(t *testing.T) {
	V1, V2, V3 := historyVersions(t)

	t.Run("create a table the store does not have; use one recorded at the newest version", func(t *testing.T) {
		plan, err := PlanTable(V3, nil, "app.store")
		must(t, err)
		qEqual(t, plan, TablePlan{Kind: PlanCreate})
		plan, err = PlanTable(V3, recordedAs(V3, StateReady), "app.store")
		must(t, err)
		qEqual(t, plan, TablePlan{Kind: PlanReady})
	})

	t.Run("migrate through every later version, oldest first; resume a version recorded migrating", func(t *testing.T) {
		plan, err := PlanTable(V3, recordedAs(V1, StateReady), "app.store")
		must(t, err)
		qEqual(t, plan, TablePlan{Kind: PlanMigrate, Versions: []*TableDefinition{V2, V3}, First: FirstBegin})
		plan, err = PlanTable(V3, recordedAs(V2, StateMigrating), "app.store")
		must(t, err)
		qEqual(t, plan, TablePlan{Kind: PlanMigrate, Versions: []*TableDefinition{V2, V3}, First: FirstResume})
	})
}

func TestPlanRefusals(t *testing.T) {
	V1, V2, V3 := historyVersions(t)
	notes := func(field string) *TableDefinition {
		return qDefine(t, "notes", TableOptions{
			Version: 1,
			Columns: []ColumnProp{Col("id", Str()), Col("meta", Json(contracts.Obj(contracts.Prop(field, contracts.Int())), JsonColumnOptions{Version: 1}))},
			Key:     []string{"id"},
		})
	}
	for _, c := range []struct {
		name       string
		definition *TableDefinition
		record     *TableRecord
		message    string
	}{
		{"a newer version (old code)", V2, recordedAs(V3, StateReady), "version 3, newer than the code's 2"},
		{
			"a version older than a new base",
			qDefine(t, "edits", TableOptions{Version: 7, Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"}}),
			recordedAs(V3, StateReady), "older than the history's base 7",
		},
		{
			"a history edited in place",
			qEvolve(t, V1, func(e *TableEditor) *TableEditor { return e.Add("dir", Str(contracts.Default("x"))) }),
			recordedAs(V2, StateReady), "recorded with another definition",
		},
		{
			"a json(...) column with another schema at the same version (D9)",
			notes("b"), recordedAs(notes("a"), StateReady), "recorded with another definition",
		},
		{"a migrating version without a data step", V3, recordedAs(V3, StateMigrating), "has no data step to resume"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := PlanTable(c.definition, c.record, "app.store")
			qFails(t, err, c.message)
		})
	}
}
