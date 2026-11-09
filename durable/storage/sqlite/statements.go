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
	"sync/atomic"
	"unicode/utf8"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// cell is a value of a stored cell, as Durable's rows hold them: nil, int64 or string.
type cell = SqliteValue

// appendCell appends JSON.stringify of a cell, or of an array of cells, to dst.
func appendCell(dst []byte, value any) []byte {
	switch typed := value.(type) {
	case nil:
		return append(dst, "null"...)
	case int64:
		return strconv.AppendInt(dst, typed, 10)
	case int:
		return strconv.AppendInt(dst, int64(typed), 10)
	case string:
		return jsstring.AppendString(dst, typed)
	case []cell:
		dst = append(dst, '[')
		for index, item := range typed {
			if index > 0 {
				dst = append(dst, ',')
			}
			dst = appendCell(dst, item)
		}
		return append(dst, ']')
	}
	panic("durable sqlite: a cell is nil, an integer, a string or an array of them")
}

// jsonTextLength is the JavaScript length of JSON text appendCell wrote: valid UTF-8, a UTF-16 unit for each character below
// U+10000 and two for each above.
func jsonTextLength(text []byte) int {
	units := 0
	for _, b := range text {
		if b&0xC0 != 0x80 {
			units++
			if b >= 0xF0 {
				units++
			}
		}
	}
	return units
}

// maxArrayCharacters: Durable Objects refuse a string over 2 MB, a bound parameter too; 512 Ki characters are at most
// 1.5 MB of UTF-8.
const maxArrayCharacters = 512 * 1024

// jsonArrayBuilder writes values as JSON arrays of at most maxArrayCharacters each (a longer value alone), in order: the
// texts of the values are appended to the array being written, with no string of their own.
type jsonArrayBuilder struct {
	arrays []string
	// array is the array being written: its opening bracket, then its parts with a comma between each.
	array []byte
	parts int
	// length is the JavaScript length of the array so far, its brackets and commas counted.
	length int
}

// newJSONArrayBuilder starts a builder whose first array has room for capacity bytes.
func newJSONArrayBuilder(capacity int) jsonArrayBuilder {
	array := make([]byte, 1, 2+min(capacity, 2*maxArrayCharacters))
	array[0] = '['
	return jsonArrayBuilder{array: array, length: 2}
}

// add appends a part, the text of value, to the array being written. It stays in this array unless that would take the
// array over the bound, and then it opens the next one.
func (builder *jsonArrayBuilder) add(value any) {
	if builder.parts > 0 {
		builder.array = append(builder.array, ',')
	}
	start := len(builder.array)
	builder.array = appendCell(builder.array, value)
	units := jsonTextLength(builder.array[start:])
	if builder.parts > 0 && builder.length+1+units > maxArrayCharacters {
		next := make([]byte, 1, 2+min(len(builder.array)-start, 2*maxArrayCharacters))
		next[0] = '['
		next = append(next, builder.array[start:]...)
		// The comma before the part becomes the closing bracket.
		builder.array[start-1] = ']'
		builder.arrays = append(builder.arrays, bytesString(builder.array[:start]))
		builder.array, builder.parts, builder.length = next, 0, 2
	} else if builder.parts > 0 {
		builder.length++
	}
	builder.length += units
	builder.parts++
}

// finish returns the arrays written. The builder is not used after it.
func (builder *jsonArrayBuilder) finish() []string {
	if builder.parts > 0 {
		builder.arrays = append(builder.arrays, bytesString(append(builder.array, ']')))
	}
	builder.array = nil
	return builder.arrays
}

// bytesString is the string over bytes that are not written again.
func bytesString(bytes []byte) string {
	return unsafe.String(unsafe.SliceData(bytes), len(bytes)) //nolint:gosec // G103: the bytes are not written again, and the string keeps them alive
}

// cellArrays is values as JSON arrays of at most maxArrayCharacters each.
func cellArrays(values []cell) []string {
	builder := newJSONArrayBuilder(16 * len(values))
	for _, value := range values {
		builder.add(value)
	}
	return builder.finish()
}

// readRows reads the rows sql gives for values, given as its one JSON array parameter (json_each(?)): one statement per
// chunk.
func readRows(executor SqliteExecutor, sql string, values []cell) ([]SqliteRow, error) {
	var rows []SqliteRow
	for _, array := range cellArrays(values) {
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
	for _, array := range cellArrays(values) {
		if err := executor.Run(sql, array); err != nil {
			return err
		}
	}
	return nil
}

// maxParameters: Durable Objects bind at most 100 parameters per statement.
const maxParameters = 100

// maxShapes is how many statement sizes fit maxParameters: 1, 2, 4 ... rows of at least two cells, up to 32 rows.
const maxShapes = 6

// insertStatement is the SQL of one table's inserts: the same texts on every call, each built when first needed.
type insertStatement struct {
	// head is INSERT INTO t (columns) VALUES, row one row of placeholders, conflict an upsert's clause.
	head, row, conflict string
	width               int
	// shapes is how many sizes fit maxParameters; texts[shape] inserts 1<<shape rows, or is nil until built.
	shapes int
	texts  [maxShapes]atomic.Pointer[string]
}

// newInsertStatement describes the inserts of table's columns, then conflict (an upsert's clause).
func newInsertStatement(table string, columns []string, conflict string) *insertStatement {
	width := len(columns)
	if width < 2 {
		panic("durable sqlite: an insert has at least two columns")
	}
	row := "(?" + strings.Repeat(", ?", width-1) + ")"
	statement := &insertStatement{
		head:     "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES ",
		row:      row,
		conflict: conflict,
		width:    width,
	}
	for rows := 1; rows*width <= maxParameters; rows *= 2 {
		statement.shapes++
	}
	return statement
}

// text is the SQL that inserts 1<<shape rows.
func (statement *insertStatement) text(shape int) string {
	if cached := statement.texts[shape].Load(); cached != nil {
		return *cached
	}
	rows := 1 << shape
	var text strings.Builder
	text.Grow(len(statement.head) + rows*(len(statement.row)+2) + len(statement.conflict) + 1)
	text.WriteString(statement.head)
	for index := range rows {
		if index > 0 {
			text.WriteString(", ")
		}
		text.WriteString(statement.row)
	}
	text.WriteByte(' ')
	text.WriteString(statement.conflict)
	built := text.String()
	statement.texts[shape].Store(&built)
	return built
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
	shape := statement.shapes - 1
	for start := 0; start < len(rows); {
		for 1<<shape > len(rows)-start {
			shape--
		}
		end := start + 1<<shape
		// The chunk's capacity ends with it: a caller keeping its parameters cannot see the next chunk's through append.
		if err := executor.Run(statement.text(shape), params[start*width:end*width:end*width]...); err != nil {
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
