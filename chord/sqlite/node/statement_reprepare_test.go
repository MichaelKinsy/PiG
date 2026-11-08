package node

import (
	"testing"

	sqlite "modernc.org/sqlite"
)

// headLookup is the shape that SQLite 3.54 plans again when its bound integer changes between 0 or 1 and any other value:
// Pi's durable storage head lookup, with the same indexes.
const headLookup = "SELECT record FROM entries WHERE conversation_id = ? AND head IS NOT NULL ORDER BY id DESC LIMIT 1"

// countingStatement opens a database with a counter of how often SQLite compiles a statement that reads entries
// (the authorizer runs on every compile, including the automatic re-prepare), and returns the counter.
func countingStatement(t *testing.T) (*DatabaseSync, func() int) {
	t.Helper()
	raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	mustDo(t, err)
	t.Cleanup(func() { mustDo(t, raw.Close()) })
	mustDo(t, raw.Exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY, conversation_id INTEGER NOT NULL, head INTEGER, record TEXT NOT NULL) STRICT;
CREATE INDEX entries_by_conversation ON entries (conversation_id, id DESC);
CREATE INDEX entry_heads_by_conversation ON entries (conversation_id, id DESC) WHERE head IS NOT NULL;
INSERT INTO entries (conversation_id, head, record) VALUES (1, 1, 'one'), (2, 1, 'two'), (7, NULL, 'seven'), (8, 1, 'eight')`))
	compiles := 0
	conn, err := raw.connection()
	mustDo(t, err)
	mustDo(t, conn.Raw(func(driverConn any) error {
		return driverConn.(sqlite.AuthorizerRegisterer).RegisterAuthorizer(
			func(action sqlite.AuthorizerActionCode, table, column, _, _ string) sqlite.AuthorizerReturnCode {
				if action == sqlite.AuthRead && table == "entries" && column == "record" {
					compiles++
				}
				return sqlite.AuthorizerOK
			})
	}))
	return raw, func() int { return compiles }
}

func TestStatementIsNotPlannedAgainWhenAnIntegerCrossesZeroAndOne(t *testing.T) {
	raw, compiles := countingStatement(t)
	statement, err := raw.Prepare(headLookup)
	mustDo(t, err)
	ids := []int64{1, 8, 1, 2, 0, 7, 1, 8}
	wantRecord := map[int64]any{1: "one", 8: "eight", 2: "two"}
	run := func() {
		for _, id := range ids {
			row, err := statement.Get(id)
			mustDo(t, err)
			if want, ok := wantRecord[id]; ok && row["record"] != want {
				t.Fatalf("conversation %d = %v, want %v", id, row, want)
			}
			if _, ok := wantRecord[id]; !ok && row != nil {
				t.Fatalf("conversation %d = %v, want no row", id, row)
			}
		}
	}
	run()
	settled := compiles()
	for range 50 {
		run()
	}
	if again := compiles() - settled; again != 0 {
		t.Fatalf("SQLite compiled the statement %d more times over 50 passes that cross 0/1; it should not plan it again", again)
	}
}

func TestStatementAlwaysRunWithSmallIntegersKeepsOneCompiledCopy(t *testing.T) {
	raw, _ := countingStatement(t)
	prepared, err := raw.Prepare(headLookup)
	mustDo(t, err)
	for _, id := range []any{true, false, int64(1), int64(0), 1, 0} {
		_, err := prepared.Get(id)
		mustDo(t, err)
	}
	if variants := len(prepared.variants); variants != 0 {
		t.Fatalf("a statement always run with 0/1 parameters compiled %d extra copies", variants)
	}
}
