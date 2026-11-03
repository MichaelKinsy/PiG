package store

// Field survival of TableDefinition: what DefineTable, EvolveTable and NewTableDefinition are given comes back from the
// definition, in the form Pi's defineTable materializes (`schemas` as declared, `byName`, `indexes`, `refs`, `column`,
// `evolution`).

import "testing"

func TestTableDefinitionRegistrationKeepsSchemasByNameIndexesRefsColumnAndEvolution(t *testing.T) {
	columns := tableColumns()
	indexes := IndexRecords{{Name: "by_path", Columns: []string{"path", "at"}}, {Name: "by_at", Columns: []string{"at"}}}
	base := qDefine(t, "survive.edits", TableOptions{Version: 3, Columns: columns, Key: []string{"id"}, Indexes: indexes})
	// readBack is the declaration a definition stands for: DefineTable's options come back from the definition.
	readBack := func(d *TableDefinition) TableOptions {
		return TableOptions{Version: d.Version, Columns: d.Schemas, Key: d.Key, Indexes: d.Indexes}
	}
	qEqual(t, readBack(base), TableOptions{Version: 3, Columns: columns, Key: []string{"id"}, Indexes: indexes})

	// Schemas are the columns as declared, in declaration order; a copy, not the caller's slice.
	qEqual(t, base.Schemas, columns)
	columns[0] = Col("changed", Str())
	qEqual(t, base.Schemas[0].Name, "id")

	// ByName holds every column under its name, the one the ordered Columns hold.
	qEqual(t, len(base.ByName), len(base.Columns))
	for _, column := range base.Columns {
		qEqual(t, base.ByName[column.Name], column)
	}

	// Indexes keep their declared order and columns.
	qEqual(t, base.Indexes, indexes)

	// Refs is one reference per column.
	qEqual(t, len(base.Refs), len(base.Columns))
	for _, column := range base.Columns {
		ref := base.Refs[column.Name]
		if ref == nil || ref.Op != ExprCol || len(ref.Args) != 1 || ref.Args[0] != column.Name {
			t.Fatalf("Refs[%q] = %+v, want a reference to the column", column.Name, ref)
		}
	}

	// Column finds a declared column and names the use of an unknown one.
	found, err := base.Column("path", "a test")
	if err != nil {
		t.Fatal(err)
	}
	qEqual(t, found, base.ByName["path"])
	_, err = base.Column("nope", "a test")
	qFails(t, err, `survive.edits: a test: no column "nope"`)

	// A base has no Evolution; EvolveTable's version links to the version before and keeps what it did not edit.
	if base.Evolution != nil {
		t.Fatalf("a base has Evolution %+v", base.Evolution)
	}
	next := qEvolve(t, base, func(e *TableEditor) *TableEditor { return e.Add("extra", Opt(Str())) })
	if next.Evolution == nil || next.Evolution.Previous != base {
		t.Fatalf("Evolution = %+v, want the version before", next.Evolution)
	}
	qEqual(t, next.Version, 4)
	qEqual(t, next.Indexes, indexes)
	qEqual(t, next.Schemas[len(next.Schemas)-1].Name, "extra")
	qEqual(t, len(next.ByName), len(base.ByName)+1)
	if _, found := next.ByName["extra"]; !found {
		t.Fatal("ByName lost the added column")
	}
	qEqual(t, len(next.Refs), len(base.Refs)+1)
}

// NewTableDefinition takes the evolution it is given and keeps it, with every other field the options set.
func TestNewTableDefinitionRegistrationKeepsTheEvolutionItIsGiven(t *testing.T) {
	previous := qDefine(t, "survive.chain", TableOptions{Version: 1, Columns: tableColumns(), Key: []string{"id"}})
	evolution := &Evolution{Previous: previous}
	definition, err := NewTableDefinition("survive.chain", TableOptions{
		Version: 2, Columns: tableColumns(), Key: []string{"id"}, Indexes: IndexRecords{{Name: "by_at", Columns: []string{"at"}}},
	}, evolution)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Evolution != evolution || definition.Evolution.Previous != previous {
		t.Fatalf("Evolution = %p, want the one given (%p)", definition.Evolution, evolution)
	}
	qEqual(t, definition.Version, 2)
	qEqual(t, definition.Indexes, IndexRecords{{Name: "by_at", Columns: []string{"at"}}})
	qEqual(t, definition.Schemas, tableColumns())
}
