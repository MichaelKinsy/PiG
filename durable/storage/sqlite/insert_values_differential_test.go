package sqlite

// The VALUES inserts of statements.go write the rows Pi's json_each inserts write. This differential test runs random
// batches through both on two databases and compares every stored value: its storage class and its bytes.

import (
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	chordnode "github.com/MichaelKinsy/PiG/chord/sqlite/node"
)

// jsonInsertText is Pi's insert of rows of little text (statements.ts): the rows as one JSON array parameter read
// through json_each(?).
func jsonInsertText(table string, columns []string, conflict string) string {
	cells := make([]string, len(columns))
	for index := range columns {
		cells[index] = "json_extract(item.value, '$[" + strconv.Itoa(index) + "]')"
	}
	return "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") SELECT " + strings.Join(cells, ", ") +
		" FROM json_each(?) AS item WHERE true " + conflict
}

// largeRowCharacters is Pi's bound: rows whose string cells hold more characters than this go bound, the others
// through the JSON array.
const largeRowCharacters = 8 * 1024

// insertRowsOfPi inserts rows as Pi's insertRows does: the rows of little text from JSON arrays, the large ones bound.
func insertRowsOfPi(executor SqliteExecutor, insertText, table string, columns []string, conflict string, rows [][]cell) error {
	var small []string
	var large [][]cell
	for _, row := range rows {
		text := 0
		for _, item := range row {
			if value, ok := item.(string); ok {
				text += utf16Length(value)
			}
		}
		if text > largeRowCharacters {
			large = append(large, row)
		} else {
			small = append(small, jsonText(row))
		}
	}
	for _, array := range jsonArrays(small) {
		if err := executor.Run(insertText, array); err != nil {
			return err
		}
	}
	statement := newInsertStatement(table, columns, conflict)
	return insertRows(executor, statement, large)
}

// insertRowsOfJSON inserts every row through the JSON array, whatever its size.
func insertRowsOfJSON(executor SqliteExecutor, insertText string, rows [][]cell) error {
	small := make([]string, len(rows))
	for index, row := range rows {
		small[index] = jsonText(row)
	}
	for _, array := range jsonArrays(small) {
		if err := executor.Run(insertText, array); err != nil {
			return err
		}
	}
	return nil
}

// tableSpec is a table of the differential test: its columns, its upsert clause and a generator of its rows.
type tableSpec struct {
	table    string
	columns  []string
	conflict string
	// row makes one row; sequence numbers the rows of the whole test, for primary keys that must not repeat.
	row func(random *rand.Rand, sequence *int64) []cell
}

var (
	// textPieces are the strings the cells are built of: JSON and SQL metacharacters, controls, NUL, every UTF-8 length,
	// surrogates (well formed in pairs, alone as WTF-8), ill-formed UTF-8, and text that reads as a number.
	textPieces = []string{
		"", "a", "Z", " ", "é", "日本語", "😀", "\u2028\u2029", "\x00", "a\x00b", `"`, `\`, `\u0041`, "\n\r\t\b\f", "\x1f", "\x7f", "\u0080", "\ufffd",
		"\ufeff", "\U0010ffff", "\ud7ff", "\ue000", "\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xff", "\xc0\xaf", "\xe2\x82", "\xf0\x9f\x98",
		"123", "-1", "1e3", "0x1f", " 7", "-0", "1.0", "9223372036854775807", "9223372036854775808", "NaN", "null", "true", "[1,2]", `{"a":1}`, "%", "'", "?", "$[0]",
		"e\u0301", "\u202e", "🏳️‍🌈",
	}
	integers = []int64{0, 1, -1, 2, 255, 256, 1 << 31, -(1 << 31), 1<<53 - 1, 1 << 53, 1<<53 + 1, -(1 << 53), math.MaxInt64, math.MinInt64, math.MaxInt64 - 1}
)

func randomText(random *rand.Rand) string {
	var out strings.Builder
	for range random.IntN(5) {
		out.WriteString(textPieces[random.IntN(len(textPieces))])
	}
	if random.IntN(6) == 0 {
		// Bytes from the ends of every UTF-8 length, leads and continuations of every kind, surrogates' leads included: any
		// ill-formed run, and any pairing of well-formed pieces into one.
		garbage := []byte{0x80, 0xbf, 0xc0, 0xc2, 0xdf, 0xe0, 0xa0, 0xe1, 0xec, 0xed, 0x9f, 0xa0, 0xaf, 0xb0, 0xbf, 0xee, 0xf0, 0x90, 0xf4, 0x8f, 0x9f, 0xf5, 0xff, 'a', 0x00}
		for range random.IntN(10) {
			out.WriteByte(garbage[random.IntN(len(garbage))])
		}
	}
	switch random.IntN(40) {
	case 0:
		// Around Pi's 8 Ki character bound, counted in UTF-16 units: an astral character is two.
		target := largeRowCharacters - 3 + random.IntN(7)
		for units := utf16Length(out.String()); units < target; {
			if random.IntN(3) == 0 && units+2 <= target {
				out.WriteString("😀")
				units += 2
			} else {
				out.WriteByte(byte('a' + random.IntN(26)))
				units++
			}
		}
	case 1:
		out.WriteString(strings.Repeat(textPieces[random.IntN(len(textPieces))], 1000))
	}
	return out.String()
}

func randomInteger(random *rand.Rand) int64 {
	if random.IntN(3) == 0 {
		return integers[random.IntN(len(integers))]
	}
	return random.Int64N(1 << 40)
}

// randomCell is any cell: NULL, an integer or a string.
func randomCell(random *rand.Rand) cell {
	switch random.IntN(4) {
	case 0:
		return nil
	case 1:
		if random.IntN(5) == 0 {
			return int(random.IntN(1000))
		}
		return randomInteger(random)
	}
	return randomText(random)
}

func maybeNull(random *rand.Rand, value cell) cell {
	if random.IntN(3) == 0 {
		return nil
	}
	return value
}

// jsonRecord is valid JSON text around text: the record of a row. Controls and quotes are escaped; everything else is
// written as it is, so ill-formed UTF-8 and surrogates reach SQLite inside a record too.
func jsonRecord(random *rand.Rand) string {
	var out strings.Builder
	out.WriteString(`{"v":"`)
	text := randomText(random)
	for index := range len(text) {
		switch b := text[index]; {
		case b == '"' || b == '\\':
			out.WriteByte('\\')
			out.WriteByte(b)
		case b < 0x20:
			fmt.Fprintf(&out, `\u%04x`, b)
		default:
			out.WriteByte(b)
		}
	}
	out.WriteString(`","n":`)
	out.WriteString(strconv.FormatInt(random.Int64N(1000), 10))
	out.WriteString(`}`)
	return out.String()
}

// firstDifference describes where two dumps differ: the first line that does, around its first differing byte.
func firstDifference(got, want []string) string {
	for index := range min(len(got), len(want)) {
		if got[index] == want[index] {
			continue
		}
		at := 0
		for at < min(len(got[index]), len(want[index])) && got[index][at] == want[index][at] {
			at++
		}
		window := func(line string) string { return line[max(at-60, 0):min(at+60, len(line))] }
		return fmt.Sprintf("row %d at hex digit %d\nVALUES: %s\nPi:     %s", index, at, window(got[index]), window(want[index]))
	}
	return fmt.Sprintf("%d rows against %d", len(got), len(want))
}

func pick[T any](random *rand.Rand, values ...T) T { return values[random.IntN(len(values))] }

var tableSpecs = []tableSpec{
	{
		table: "record_ids", columns: []string{"id", "record_type"}, conflict: "ON CONFLICT(id) DO NOTHING",
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(60), pick(random, "conversation", "entry", "task", "submission", "document")}
		},
	},
	{
		table: "conversations", columns: []string{"id", "owner_conversation_id", "owner_task_id", "record"},
		row: func(random *rand.Rand, sequence *int64) []cell {
			*sequence++
			return []cell{*sequence, maybeNull(random, randomInteger(random)), maybeNull(random, randomInteger(random)), jsonRecord(random)}
		},
	},
	{
		table: "entries", columns: []string{"id", "conversation_id", "head", "commit_seq", "record"},
		row: func(random *rand.Rand, sequence *int64) []cell {
			*sequence++
			return []cell{*sequence, randomInteger(random), maybeNull(random, randomInteger(random)), randomInteger(random), jsonRecord(random)}
		},
	},
	{
		table: "tasks", columns: []string{"id", "conversation_id", "kind", "status", "abort_requested", "background", "record"},
		conflict: `ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id, kind = excluded.kind,
				status = excluded.status, abort_requested = excluded.abort_requested,
				background = excluded.background, record = excluded.record`,
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(40), randomInteger(random), randomText(random), pick(random, "pending", "running", "waiting", "sleeping", "completing", "terminal"),
				int64(random.IntN(2)), int64(random.IntN(2)), jsonRecord(random)}
		},
	},
	{
		table: "submissions", columns: []string{"id", "conversation_id", "request_id", "status", "record"},
		conflict: `ON CONFLICT(id) DO UPDATE SET conversation_id = excluded.conversation_id,
				request_id = excluded.request_id, status = excluded.status, record = excluded.record`,
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(40), randomInteger(random), maybeNull(random, randomText(random)), pick(random, "queued", "placed", "done", "unanswered"), jsonRecord(random)}
		},
	},
	{
		table:    "documents",
		columns:  []string{"id", "kind", "family", "key_value", "scope_kind", "owner_id", "created_at", "retired_at", "record"},
		conflict: "ON CONFLICT(id) DO UPDATE SET retired_at = excluded.retired_at, record = excluded.record",
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(40), randomText(random), int64(random.IntN(2)), randomText(random), pick(random, "session", "conversation", "task"),
				randomInteger(random), randomInteger(random), maybeNull(random, randomInteger(random)), jsonRecord(random)}
		},
	},
	{
		table: "document_revisions", columns: []string{"document_id", "seq", "kind", "version", "content"},
		row: func(random *rand.Rand, sequence *int64) []cell {
			*sequence++
			return []cell{random.Int64N(40), *sequence, pick(random, "base", "delta"), randomInteger(random), jsonRecord(random)}
		},
	},
	// Scratch tables hold any cell in every storage class and affinity.
	{
		table: "loose", columns: []string{"id", "t", "i", "n", "b", "a"}, conflict: "ON CONFLICT(id) DO UPDATE SET t = excluded.t, i = excluded.i, n = excluded.n, b = excluded.b, a = excluded.a",
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(50), randomCell(random), randomCell(random), randomCell(random), randomCell(random), randomCell(random)}
		},
	},
	{
		table: "strict_any", columns: []string{"id", "t", "i", "a"}, conflict: "ON CONFLICT(id) DO NOTHING",
		row: func(random *rand.Rand, sequence *int64) []cell {
			return []cell{random.Int64N(50), randomText(random), randomInteger(random), randomCell(random)}
		},
	},
}

const scratchTables = `
	CREATE TABLE loose (id INTEGER PRIMARY KEY, t TEXT, i INTEGER, n NUMERIC, b BLOB, a);
	CREATE TABLE strict_any (id INTEGER PRIMARY KEY, t TEXT, i INTEGER, a ANY) STRICT;
`

// openDifferentialDatabase is a database with the durable schema (under its default table names) and the scratch tables.
func openDifferentialDatabase(t *testing.T) *chordnode.NodeSqliteDatabase {
	t.Helper()
	database, err := chordnode.OpenNodeSqliteDatabase(filepath.Join(t.TempDir(), "storage.sqlite"), chordnode.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	storage, err := Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close(t.Context()) })
	if err := database.Exec(scratchTables); err != nil {
		t.Fatal(err)
	}
	return database
}

// dump is every row of table, each cell as its storage class and the hex of its bytes.
func dump(t *testing.T, database SqliteExecutor, spec tableSpec) []string {
	t.Helper()
	cells := make([]string, len(spec.columns))
	for index, column := range spec.columns {
		cells[index] = "typeof(" + column + ") || ':' || hex(CAST(" + column + " AS BLOB))"
	}
	rows, err := database.All("SELECT " + strings.Join(cells, " || '|' || ") + " AS line FROM " + spec.table + " ORDER BY " + strings.Join(spec.columns, ", "))
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, len(rows))
	for index, row := range rows {
		lines[index] = row["line"].(string)
	}
	return lines
}

func inTransaction(database *chordnode.NodeSqliteDatabase, write func(SqliteExecutor) error) error {
	return database.Transaction(func(transaction SqliteExecutor) error { return write(transaction) })
}

func TestValuesInsertsStoreTheRowsOfPiJsonEachInserts(t *testing.T) {
	for index, spec := range tableSpecs {
		t.Run(spec.table, func(t *testing.T) {
			values, reference, forced := openDifferentialDatabase(t), openDifferentialDatabase(t), openDifferentialDatabase(t)
			statement := newInsertStatement(spec.table, spec.columns, spec.conflict)
			insertText := jsonInsertText(spec.table, spec.columns, spec.conflict)
			random := rand.New(rand.NewPCG(uint64(index)+1, 0x5eed))
			var sequence int64
			for batch := range 150 {
				size := random.IntN(24)
				switch random.IntN(20) {
				case 0:
					size = 100 + random.IntN(200)
				case 1:
					size = 0
				}
				rows := make([][]cell, size)
				large := false
				for row := range rows {
					rows[row] = spec.row(random, &sequence)
					characters := 0
					for _, value := range rows[row] {
						if text, ok := value.(string); ok {
							characters += utf16Length(text)
						}
					}
					large = large || characters > largeRowCharacters
				}
				if large && spec.conflict != "" {
					// Pi binds the large rows after the others, so it applies a key's rows in another order than the call's: a commit's
					// upserts never repeat a key (an ordered map by id keeps one row each).
					seen := map[cell]bool{}
					rows = slices.DeleteFunc(rows, func(row []cell) bool { duplicate := seen[row[0]]; seen[row[0]] = true; return duplicate })
				}
				gotErr := inTransaction(values, func(executor SqliteExecutor) error { return insertRows(executor, statement, rows) })
				wantErr := inTransaction(reference, func(executor SqliteExecutor) error {
					return insertRowsOfPi(executor, insertText, spec.table, spec.columns, spec.conflict, rows)
				})
				if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
					t.Fatalf("batch %d: VALUES error %v, Pi's %v", batch, gotErr, wantErr)
				}
				if got, want := dump(t, values, spec), dump(t, reference, spec); !slices.Equal(got, want) {
					t.Fatalf("batch %d (%d rows): the stored rows differ from Pi's: %s", batch, size, firstDifference(got, want))
				}
				// Every row through the JSON array, large or not: the stored rows are the same unless a large row, which Pi binds.
				if !large {
					wantErr := inTransaction(forced, func(executor SqliteExecutor) error { return insertRowsOfJSON(executor, insertText, rows) })
					if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
						t.Fatalf("batch %d: VALUES error %v, the JSON array's %v", batch, gotErr, wantErr)
					}
				} else if gotErr == nil {
					_ = inTransaction(forced, func(executor SqliteExecutor) error { return insertRows(executor, statement, rows) })
				}
				if got, want := dump(t, values, spec), dump(t, forced, spec); !slices.Equal(got, want) {
					t.Fatalf("batch %d (%d rows): the stored rows differ from the JSON array's: %s", batch, size, firstDifference(got, want))
				}
			}
		})
	}
}

// The inserts the commit path runs are the ones the differential test compares: same tables, columns and upserts.
func TestCommitStatementsInsertTheColumnsOfTheDifferentialSpecs(t *testing.T) {
	schema, err := CreateSqliteSchema(nil)
	if err != nil {
		t.Fatal(err)
	}
	statements := newCommitStatements(schema.Tables)
	for _, committed := range []struct {
		spec      tableSpec
		statement *insertStatement
	}{
		{tableSpecs[0], statements.recordIds}, {tableSpecs[1], statements.conversations}, {tableSpecs[2], statements.entries},
		{tableSpecs[3], statements.tasks}, {tableSpecs[4], statements.submissions}, {tableSpecs[5], statements.documents}, {tableSpecs[6], statements.revisions},
	} {
		table := map[string]string{
			"record_ids": schema.Tables.RecordIds, "conversations": schema.Tables.Conversations, "entries": schema.Tables.Entries, "tasks": schema.Tables.Tasks,
			"submissions": schema.Tables.Submissions, "documents": schema.Tables.Documents, "document_revisions": schema.Tables.DocumentRevisions,
		}[committed.spec.table]
		want := newInsertStatement(table, committed.spec.columns, committed.spec.conflict)
		for shape := range want.shapes {
			if got, want := committed.statement.text(shape), want.text(shape); got != want {
				t.Errorf("%s: the commit path's insert of %d rows differs from the spec's:\n%q\n%q", committed.spec.table, 1<<shape, got, want)
			}
		}
	}
}
