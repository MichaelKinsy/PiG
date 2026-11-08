package sqlite

// Ports packages/durable/src/storage/sqlite/statements.ts
//
// The multi-row statements of a commit call, few whatever the number of rows. Reads and deletions take their keys as one
// JSON array parameter through json_each(?) (one SQL text per query, no bound-parameter limit). Inserts bind every cell:
// multi-row VALUES of a few fixed sizes (1, 2, 4 ... rows), so no row is JSON-encoded to be decoded again by SQLite and a
// record is not escaped a second time. Pi inserts the rows of little text from one JSON array parameter through
// json_each(?) and binds only the large ones; the stored rows are the same either way (insertRowsOfJSON in the tests is
// Pi's statement, and a differential test compares the two).

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// cell is a value of a stored cell, as Durable's rows hold them: nil, int64 or string.
type cell = SqliteValue

// jsonText is JSON.stringify of a cell, or of an array of cells.
func jsonText(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	case string:
		return encodeIndexedString(typed)
	case []cell:
		var out strings.Builder
		out.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				out.WriteByte(',')
			}
			out.WriteString(jsonText(item))
		}
		out.WriteByte(']')
		return out.String()
	}
	panic("durable sqlite: a cell is nil, an integer, a string or an array of them")
}

// utf16Length is a string's JavaScript length.
func utf16Length(text string) int {
	units := 0
	for rest := text; rest != ""; {
		r, size := jsstring.DecodeCodePoint(rest)
		units++
		if r >= 0x10000 {
			units++
		}
		//portlint:allow utf16units advances by the byte size of the code point just decoded; the count is UTF-16 units
		rest = rest[size:]
	}
	return units
}

// readRows reads the rows sql gives for values, given as its one JSON array parameter (json_each(?)): one statement per
// chunk.
func readRows(executor SqliteExecutor, sql string, values []cell) ([]SqliteRow, error) {
	var rows []SqliteRow
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = jsonText(value)
	}
	for _, array := range jsonArrays(parts) {
		chunk, err := executor.All(sql, array)
		if err != nil {
			return nil, err
		}
		rows = append(rows, chunk...)
	}
	return rows, nil
}

// runForEach runs sql (a deletion) for values, given as its one JSON array parameter: one statement per chunk.
func runForEach(executor SqliteExecutor, sql string, values []cell) error {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = jsonText(value)
	}
	for _, array := range jsonArrays(parts) {
		if err := executor.Run(sql, array); err != nil {
			return err
		}
	}
	return nil
}

// maxParameters: Durable Objects bind at most 100 parameters per statement.
const maxParameters = 100

// insertStatement is the SQL of one table's inserts: the same texts on every call, so built once per schema.
type insertStatement struct {
	// texts[shape] inserts 1<<shape rows, bound column by column: INSERT INTO t (columns) VALUES (?, ...), ... conflict.
	texts []string
	width int
}

// newInsertStatement builds the inserts of table's columns, then conflict (an upsert's clause).
func newInsertStatement(table string, columns []string, conflict string) insertStatement {
	width := len(columns)
	placeholders := make([]string, width)
	for index := range placeholders {
		placeholders[index] = "?"
	}
	row := "(" + strings.Join(placeholders, ", ") + ")"
	head := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES "
	var texts []string
	for rows := 1; rows*width <= maxParameters; rows *= 2 {
		values := make([]string, rows)
		for index := range values {
			values[index] = row
		}
		texts = append(texts, head+strings.Join(values, ", ")+" "+conflict)
	}
	return insertStatement{texts: texts, width: width}
}

// insertRows inserts rows (one cell per column, in order) with statement, bound column by column in statements of 1, 2,
// 4, ... rows up to maxParameters cells, the largest first: few statements, and at most a handful of SQL texts per table
// for prepared statements.
func insertRows(executor SqliteExecutor, statement *insertStatement, rows [][]cell) error {
	if len(rows) == 0 {
		return nil
	}
	width := statement.width
	params := make([]SqliteValue, 0, len(rows)*width)
	for _, row := range rows {
		for _, item := range row {
			if text, ok := item.(string); ok {
				item = wellFormed(text)
			}
			params = append(params, item)
		}
	}
	// shape only shrinks: lowered before a chunk to fit the rows left, it is also what the loop then steps over.
	shape := len(statement.texts) - 1
	for start := 0; start < len(rows); {
		for 1<<shape > len(rows)-start {
			shape--
		}
		end := start + 1<<shape
		// The chunk's capacity ends with it: a caller keeping its parameters cannot see the next chunk's through append.
		if err := executor.Run(statement.texts[shape], params[start*width:end*width:end*width]...); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// wellFormed is text as a JavaScript string holds it once stored: what SQLite reads back from the text JSON.stringify
// writes for it (the rows of little text in Pi's inserts). Valid UTF-8 is as it is. The WTF-8 bytes of a lone UTF-16
// surrogate stay, but a high one directly followed by a low one is one character, and each maximal ill-formed subpart is one
// U+FFFD, where binding the bytes would store them raw.
func wellFormed(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	out := make([]byte, 0, len(text)+8)
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r != utf8.RuneError || size != 1 {
			out = append(out, text[i:i+size]...)
			i += size
			continue
		}
		if high, ok := wtf8Surrogate(text[i:]); ok {
			if low, ok := wtf8Surrogate(text[i+3:]); ok && high < 0xdc00 && low >= 0xdc00 {
				out = utf8.AppendRune(out, 0x10000+(high-0xd800)<<10+low-0xdc00)
				i += 6
				continue
			}
			out = append(out, text[i:i+3]...)
			i += 3
			continue
		}
		out = append(out, "\ufffd"...)
		i += maximalSubpart(text, i)
	}
	return string(out)
}

// wtf8Surrogate is the UTF-16 surrogate unit the three WTF-8 bytes at the start of text spell.
func wtf8Surrogate(text string) (rune, bool) {
	if len(text) < 3 || text[0] != 0xed || text[1] < 0xa0 || text[1] > 0xbf || text[2] < 0x80 || text[2] > 0xbf {
		return 0, false
	}
	return rune(text[0]&15)<<12 | rune(text[1]&63)<<6 | rune(text[2]&63), true
}

// maximalSubpart is the length of the ill-formed sequence at text[i] that a WHATWG UTF-8 decoder (Node's Buffer and
// TextDecoder) replaces with one U+FFFD: a lead byte with the continuation bytes it accepts before the sequence breaks off,
// or one byte that cannot start a sequence.
func maximalSubpart(text string, i int) int {
	need, lo, hi := 0, byte(0x80), byte(0xbf)
	switch c := text[i]; {
	case c >= 0xc2 && c <= 0xdf:
		need = 2
	case c == 0xe0:
		need, lo = 3, 0xa0
	case c == 0xed:
		need, hi = 3, 0x9f
	case c >= 0xe1 && c <= 0xef:
		need = 3
	case c == 0xf0:
		need, lo = 4, 0x90
	case c >= 0xf1 && c <= 0xf3:
		need = 4
	case c == 0xf4:
		need, hi = 4, 0x8f
	default:
		return 1
	}
	n := 1
	if i+n < len(text) && text[i+n] >= lo && text[i+n] <= hi {
		n++
		for n < need && i+n < len(text) && text[i+n]&0xc0 == 0x80 {
			n++
		}
	}
	return n
}

// maxArrayCharacters: Durable Objects refuse a string over 2 MB, a bound parameter too; 512 Ki characters are at most
// 1.5 MB of UTF-8.
const maxArrayCharacters = 512 * 1024

// jsonArrays is the JSON values parts, in order, as JSON arrays of at most maxArrayCharacters each (a longer value alone).
func jsonArrays(parts []string) []string {
	var arrays []string
	var chunk []string
	// The array's text: its brackets, its parts and the commas between them.
	length := 2
	for _, part := range parts {
		units := utf16Length(part)
		if len(chunk) > 0 && length+1+units > maxArrayCharacters {
			arrays = append(arrays, "["+strings.Join(chunk, ",")+"]")
			chunk = nil
			length = 2
		}
		if len(chunk) > 0 {
			length++
		}
		length += units
		chunk = append(chunk, part)
	}
	if len(chunk) > 0 {
		arrays = append(arrays, "["+strings.Join(chunk, ",")+"]")
	}
	return arrays
}
