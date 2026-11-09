package node

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/third_party/sqlite"
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
	mustDo(t, conn.(sqlite.AuthorizerRegisterer).RegisterAuthorizer(
		func(action sqlite.AuthorizerActionCode, table, column, _, _ string) sqlite.AuthorizerReturnCode {
			if action == sqlite.AuthRead && table == "entries" && column == "record" {
				compiles++
			}
			return sqlite.AuthorizerOK
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

func TestStatementCopiesAreCappedAndRowsStayRight(t *testing.T) {
	raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	mustDo(t, err)
	t.Cleanup(func() { mustDo(t, raw.Close()) })
	statement, err := raw.Prepare("SELECT ? + ? * 2 + ? * 4 + ? * 8 + ? * 16 + ? * 32 + ? * 64 + ? * 128 AS n")
	mustDo(t, err)
	for round := range 3 {
		for pattern := range 256 {
			params := make([]any, 8)
			want := int64(0)
			for bit := range 8 {
				value := int64(pattern >> bit & 1)
				if value == 1 && round == 1 {
					value = 5 // the same positions with a non-0/1 value
				}
				params[bit] = value
				want += value << bit
			}
			row, err := statement.Get(params...)
			mustDo(t, err)
			if row["n"] != want {
				t.Fatalf("pattern %08b round %d = %v, want %d", pattern, round, row["n"], want)
			}
		}
	}
	if copies := len(statement.variants); copies > maxStatementVariants {
		t.Fatalf("a statement run with every 0/1 pattern keeps %d extra copies, want at most %d", copies, maxStatementVariants)
	}
}

func TestInsertKeepsNoExtraCopies(t *testing.T) {
	raw, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	mustDo(t, err)
	t.Cleanup(func() { mustDo(t, raw.Close()) })
	mustDo(t, raw.Exec("CREATE TABLE t (a INTEGER, b INTEGER, c INTEGER)"))
	statement, err := raw.Prepare("INSERT INTO t (a, b, c) VALUES (?, ?, ?)")
	mustDo(t, err)
	for pattern := range 27 {
		mustDo(t, statement.Run(int64(pattern%3), int64(pattern/3%3), int64(pattern/9)))
	}
	if copies := len(statement.variants); copies != 0 {
		t.Fatalf("an INSERT keeps %d extra copies, want none", copies)
	}
}

func TestReadsRows(t *testing.T) {
	for sqlText, want := range map[string]bool{
		"SELECT 1": true, "  select 1": true, "\nWith x AS (SELECT 1) SELECT * FROM x": true, "SELECT(1)": true,
		"INSERT INTO t VALUES (1)": false, "UPDATE t SET a = 1": false, "SELECTED": false, "": false, "WITHOUT": false,
	} {
		if got := readsRows(sqlText); got != want {
			t.Errorf("readsRows(%q) = %v, want %v", sqlText, got, want)
		}
	}
}
