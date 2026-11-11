package sqlite

import (
	"strings"
	"testing"
)

// Durable Objects bind at most 100 parameters per statement: each table's largest statement fills them without passing 100,
// and one more doubling would.
func TestInsertStatementsStopAtTheHundredParameterBound(t *testing.T) {
	schema, err := CreateSqliteSchema(nil)
	if err != nil {
		t.Fatal(err)
	}
	statements := newCommitStatements(schema.Tables)
	for name, statement := range map[string]*insertStatement{
		"record_ids": statements.recordIds, "conversations": statements.conversations, "entries": statements.entries, "tasks": statements.tasks,
		"submissions": statements.submissions, "documents": statements.documents, "document_revisions": statements.revisions,
	} {
		largest := statement.shapes - 1
		if rows := 1 << largest; rows*statement.width > 100 || 2*rows*statement.width <= 100 {
			t.Errorf("%s: the largest statement has %d rows of %d cells, want the most within 100 parameters", name, rows, statement.width)
		}
		if got := strings.Count(statement.text(largest), "?"); got != (1<<largest)*statement.width {
			t.Errorf("%s: the largest statement has %d placeholders, want %d", name, got, (1<<largest)*statement.width)
		}
	}
	if statements.documents.width != 9 || statements.documents.shapes != 4 {
		t.Errorf("documents: %d cells in %d sizes, want 9 cells in 4 sizes (8 rows at most)", statements.documents.width, statements.documents.shapes)
	}
}
