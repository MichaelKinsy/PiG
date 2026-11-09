package sqlite

// pi: packages/durable/src/storage/sqlite/statements.ts
//
// Literal bytes of the rows insertRows stores, against Pi's insertRows itself (statements.ts imported as-is under Node, over
// node:sqlite). Pi inserts the rows of up to 8 Ki characters through JSON.stringify and json_each (a lone surrogate is kept
// as the three bytes of its WTF-8) and binds the larger ones (node:sqlite writes U+FFFD for a lone surrogate). Text with
// lone surrogates, a surrogate pair split in two units and ill-formed UTF-8 is run at each side of the bound.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type oracleBatch struct {
	Ddl      string   `json:"ddl"`
	Table    string   `json:"table"`
	Columns  []string `json:"columns"`
	Conflict string   `json:"conflict"`
	Rows     [][]any  `json:"rows"`
}

type oracleSegment struct {
	B *string `json:"b,omitempty"`
	U []int   `json:"u,omitempty"`
}

type oracleText struct {
	S []oracleSegment `json:"s"`
}

// oracleCell is text as the JavaScript string Pi is given: the three bytes of a lone surrogate are its unit, any other
// bytes are decoded by Node's TextDecoder (U+FFFD for each maximal ill-formed subpart).
func oracleCell(text string) oracleText {
	segments := []oracleSegment{}
	start := 0
	flush := func(end int) {
		if end > start {
			encoded := base64.StdEncoding.EncodeToString([]byte(text[start:end]))
			segments = append(segments, oracleSegment{B: &encoded})
		}
	}
	for i := 0; i < len(text); {
		if r, size := jsstring.DecodeCodePoint(text[i:]); size == 3 && r >= 0xd800 && r <= 0xdfff {
			flush(i)
			segments = append(segments, oracleSegment{U: []int{int(r)}})
			i += 3
			start = i
			continue
		}
		i++
	}
	flush(len(text))
	return oracleText{S: segments}
}

func piInsertRows(t *testing.T, batches []oracleBatch) [][]string {
	t.Helper()
	input, err := json.Marshal(batches)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/insert_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var answers []json.RawMessage
	if err := json.Unmarshal(output, &answers); err != nil || len(answers) != len(batches) {
		t.Fatalf("oracle output: %v (%d answers for %d batches)", err, len(answers), len(batches))
	}
	out := make([][]string, len(answers))
	for i, answer := range answers {
		if err := json.Unmarshal(answer, &out[i]); err != nil {
			t.Fatalf("batch %d: Pi failed: %s", i, answer)
		}
	}
	return out
}

// rowAt is a row of the scratch table loose (id, t, a) of units UTF-16 units: t is piece among 'a's, placed by where
// (0 leading, 1 in the middle, 2 trailing), a is second.
func rowAt(piece, second string, units, where int) []cell {
	filler := max(units-utf16Length(jsView(piece))-utf16Length(jsView(second)), 0)
	cut := []int{0, filler / 2, filler}[where]
	return []cell{int64(1), strings.Repeat("a", cut) + piece + strings.Repeat("a", filler-cut), second}
}

func TestInsertRowsStoreTheBytesOfPiAtTheLargeRowBound(t *testing.T) {
	pieces := []string{
		"\xed\xa0\x80", "\xed\xb0\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xed\xb8\x80\xed\xa0\xbd", "\xed\xa0\xbd", "😀",
		"\xff", "\xc0\xaf", "\xe2\x82", "\xf0\x9f\x98", "\xed\xa0", "\xe1\xa0\xed\xa0\x80", "x\xed\xa0\x80y\xed\xb0\x80z", "\xf4\x90\x80\x80",
	}
	var batches []oracleBatch
	var rowsOf [][]cell
	for _, units := range []int{10, 8190, 8191, 8192, 8193, 8194, 20000} {
		for _, piece := range pieces {
			for _, second := range []string{"", piece} {
				for where := range 3 {
					row := rowAt(piece, second, units, where)
					cells := make([]any, len(row))
					for i, c := range row {
						if text, ok := c.(string); ok {
							cells[i] = oracleCell(text)
						} else {
							cells[i] = c
						}
					}
					batches = append(batches, oracleBatch{Ddl: scratchTables, Table: "loose", Columns: []string{"id", "t", "a"}, Rows: [][]any{cells}})
					rowsOf = append(rowsOf, row)
				}
			}
		}
	}
	want := piInsertRows(t, batches)
	columns := []string{"id", "t", "a"}
	statement := newInsertStatement("loose", columns, "")
	mismatches := 0
	for i, row := range rowsOf {
		database := openDifferentialDatabase(t)
		if err := inTransaction(database, func(executor SqliteExecutor) error { return insertRows(executor, statement, [][]cell{row}) }); err != nil {
			t.Fatal(err)
		}
		if got := dump(t, database, tableSpec{table: "loose", columns: columns}); !slices.Equal(got, want[i]) {
			mismatches++
			if mismatches <= 5 {
				text := row[1].(string)
				t.Errorf("row of %d units, first text %q ... second %q: %s", utf16Length(jsView(text))+utf16Length(jsView(row[2].(string))),
					text[max(len(text)-20, 0):], row[2], firstDifference(got, want[i]))
			}
		}
	}
	if mismatches > 0 {
		t.Errorf("%d of %d rows differ from Pi's", mismatches, len(rowsOf))
	}
}

// jsView is the JavaScript string Pi holds for text, with lone surrogates kept.
func jsView(text string) string { return wellFormed(text) }
