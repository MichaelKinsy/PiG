package store

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/store/table-history.ts
//
// A table's history (slice 6a §9; decision "Second spec review: the majority"): the versions from a base
// `DefineTable` through each `EvolveTable`, the migration each version recorded, and what an activation does with the
// version the store recorded: create the table, use it, migrate it from there, or refuse it.
//
// Go forced differences: Pi's discriminated unions (TableEdit, DataStep, TablePlan) are structs with a Kind; a
// `rows` patch is `func(Row) (any, error)` returning a SetOf or a Row (a thrown error is the returned one). Edits
// decode from their JSON (DecodeTableEdit) for archives.

// The kinds of TableEdit.
const (
	EditRename    = "rename"
	EditAdd       = "add"
	EditDrop      = "drop"
	EditJSON      = "json"
	EditIndex     = "index"
	EditDropIndex = "dropIndex"
)

// TableEdit is one edit of a migration as its writes record it (`alter`, `index`, `dropIndex`); the table is named
// when applied.
//
//	rename:    From, To
//	add, json: Name, Column
//	drop, dropIndex: Name
//	index:     Name, Columns
type TableEdit struct {
	Kind    string
	From    string
	To      string
	Name    string
	Column  ColumnRecord
	Columns []string
}

// AlterEdit is the column edits an `alter` write records: the TableEdit kinds rename, add, drop and json.
type AlterEdit = TableEdit

// DecodeTableEdit reads a TableEdit from its JSON (a decoded *delta.JsonObject).
func DecodeTableEdit(raw any) (TableEdit, error) {
	object, ok := raw.(*delta.JsonObject)
	if !ok {
		return TableEdit{}, decodeError("table edit", "expected an object")
	}
	kind, _ := object.Value("kind").(string)
	text := func(key string) (string, error) {
		value, ok := object.Value(key).(string)
		if !ok {
			return "", decodeError("table edit", fmt.Sprintf("%s: expected a string %s", kind, quote(key)))
		}
		return value, nil
	}
	edit := TableEdit{Kind: kind}
	var err error
	switch kind {
	case EditRename:
		if edit.From, err = text("from"); err != nil {
			return TableEdit{}, err
		}
		if edit.To, err = text("to"); err != nil {
			return TableEdit{}, err
		}
	case EditAdd, EditJSON:
		if edit.Name, err = text("name"); err != nil {
			return TableEdit{}, err
		}
		if edit.Column, err = ColumnRecordFromJSON(object.Value("column")); err != nil {
			return TableEdit{}, decodeError("table edit", err.Error())
		}
	case EditDrop, EditDropIndex:
		if edit.Name, err = text("name"); err != nil {
			return TableEdit{}, err
		}
	case EditIndex:
		if edit.Name, err = text("name"); err != nil {
			return TableEdit{}, err
		}
		list, ok := object.Value("columns").([]any)
		if !ok {
			return TableEdit{}, decodeError("table edit", "index: expected an array of columns")
		}
		edit.Columns = make([]string, len(list))
		for i, each := range list {
			column, ok := each.(string)
			if !ok {
				return TableEdit{}, decodeError("table edit", "index: expected column names")
			}
			edit.Columns[i] = column
		}
	default:
		return TableEdit{}, decodeError("table edit", fmt.Sprintf("unknown kind %s", quote(kind)))
	}
	return edit, nil
}

// The kinds of DataStep.
const (
	StepRows   = "rows"
	StepUpdate = "update"
)

// DataStep is a version's data step over Table, the columns between the edits before it and after it: `rows`
// patches each row (synchronous and pure), `update` is one `updateWhere`.
type DataStep struct {
	Kind  string
	Table *TableDefinition
	// Patch (rows) gets each row as the columns stand there and returns the patch: a SetOf or a Row of new values of
	// non-key columns (Undefined clears an `opt(...)` one).
	Patch func(row Row) (any, error)
	// Where (update) is the compiled condition; nil for none.
	Where *WhereNode
	// Set (update) is the compiled set.
	Set SetNode
}

// Migration is what one `EvolveTable` changed: the edits before the data step, the step, the edits after it.
type Migration struct {
	Before []TableEdit
	// Step is nil without a data step.
	Step  *DataStep
	After []TableEdit
}

// The kinds of TablePlan.
const (
	PlanCreate  = "create"
	PlanReady   = "ready"
	PlanMigrate = "migrate"
)

// The first commit of a migrating plan.
const (
	FirstBegin  = "begin"
	FirstResume = "resume"
)

// TablePlan is what an activation does with a declared table: create it at the newest version, use it as recorded, or
// migrate it through Versions (oldest first), the first one from its start (FirstBegin) or, recorded `migrating`,
// after its (A) commit (FirstResume).
type TablePlan struct {
	Kind     string
	Versions []*TableDefinition
	First    string
}

// PlanTable is the plan for definition (the code's newest version) against recorded (nil: none). A recorded version
// newer than the newest, older than the history's base, or the same version with another fingerprint (a history
// edited in place) is an error: the object fails `broken`, the data stays.
func PlanTable(definition *TableDefinition, recorded *TableRecord, what string) (TablePlan, error) {
	if recorded == nil {
		return TablePlan{Kind: PlanCreate}, nil
	}
	version, state, fingerprint := recorded.Version, recorded.State, recorded.Fingerprint
	if version > definition.Version {
		return TablePlan{}, fmt.Errorf("%s: the store has version %d, newer than the code's %d", what, version, definition.Version)
	}
	versions := historyOf(definition)
	var at *TableDefinition
	for _, each := range versions {
		if each.Version == version {
			at = each
			break
		}
	}
	if at == nil {
		return TablePlan{}, fmt.Errorf("%s: the store has version %d, older than the history's base %d", what, version, versions[0].Version)
	}
	if at.Fingerprint != fingerprint {
		return TablePlan{}, fmt.Errorf("%s: version %d is recorded with another definition (a history edited in place)", what, version)
	}
	if state == StateReady && version == definition.Version {
		return TablePlan{Kind: PlanReady}, nil
	}
	if state == StateMigrating && (at.Evolution == nil || at.Evolution.Migration.Step == nil) {
		return TablePlan{}, fmt.Errorf("%s: version %d is recorded migrating, but has no data step to resume", what, version)
	}
	var later []*TableDefinition
	if state == StateMigrating {
		for _, each := range versions {
			if each.Version >= version {
				later = append(later, each)
			}
		}
		return TablePlan{Kind: PlanMigrate, Versions: later, First: FirstResume}, nil
	}
	for _, each := range versions {
		if each.Version > version {
			later = append(later, each)
		}
	}
	return TablePlan{Kind: PlanMigrate, Versions: later, First: FirstBegin}, nil
}

// historyOf is every version of definition's history, the base first.
func historyOf(definition *TableDefinition) []*TableDefinition {
	var versions []*TableDefinition
	for at := definition; at != nil; {
		versions = append([]*TableDefinition{at}, versions...)
		if at.Evolution == nil {
			break
		}
		at = at.Evolution.Previous
	}
	return versions
}
