package store

import "testing"

// Pi store/expression.ts:66-72 `ColumnRef.column`: the column a reference names, read by scalarColumn and orderBy and by any
// callback handed a ref.
func TestColumnRefColumnIsTheNamedColumn(t *testing.T) {
	table := exprTable(t)
	if got := NewColumnRef("path").Column(); got != "path" {
		t.Errorf("NewColumnRef(path).Column() = %q", got)
	}
	for name, ref := range table.Refs {
		if got := ref.Column(); got != name {
			t.Errorf("Refs[%q].Column() = %q", name, got)
		}
	}
}

// A ref's column is not an expression's: Pi's `instanceof ColumnRef` rejects an Expression that is not a ColumnRef, so Column
// reads empty for one.
func TestColumnOfANonReferenceExpressionIsEmpty(t *testing.T) {
	if got := Add(NewColumnRef("n"), 1).Column(); got != "" {
		t.Errorf("Add(...).Column() = %q, want empty", got)
	}
}
