package sqlite

// Ports packages/durable/src/storage/sqlite/statements.ts
//
// The multi-row statements of a commit call, few whatever the number of rows: rows as one JSON array parameter read
// through json_each(?) (one SQL text per table, no bound-parameter limit), and large rows bound column by column in
// multi-row VALUES of a few fixed sizes, so their already JSON-encoded records are not escaped a second time.

import (
	"strconv"
	"strings"

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

// largeRowCharacters: rows whose string cells hold more characters than this (before escaping) go bound: a second JSON
// escaping of their records costs more than the statements they need.
const largeRowCharacters = 8 * 1024

// insertStatement is the SQL of one table's inserts: the same texts on every call, so built once per schema.
type insertStatement struct {
	// small is the insert of rows of little text from one JSON array parameter.
	small string
	// head is INSERT INTO t (columns) VALUES, for the large rows bound column by column.
	head string
	// row is one row of placeholders.
	row      string
	width    int
	conflict string
}

// newInsertStatement builds the inserts of table's columns, then conflict (an upsert's clause).
func newInsertStatement(table string, columns []string, conflict string) insertStatement {
	names := strings.Join(columns, ", ")
	cells := make([]string, len(columns))
	placeholders := make([]string, len(columns))
	for index := range columns {
		cells[index] = "json_extract(item.value, '$[" + strconv.Itoa(index) + "]')"
		placeholders[index] = "?"
	}
	return insertStatement{
		// WHERE true keeps SQLite from reading an upsert's ON as a join constraint of the SELECT.
		small:    "INSERT INTO " + table + " (" + names + ") SELECT " + strings.Join(cells, ", ") + " FROM json_each(?) AS item WHERE true " + conflict,
		head:     "INSERT INTO " + table + " (" + names + ") VALUES",
		row:      "(" + strings.Join(placeholders, ", ") + ")",
		width:    len(columns),
		conflict: conflict,
	}
}

// insertRows inserts rows (one cell per column, in order) with statement: the rows of little text from one JSON array
// parameter per chunk, the same SQL text whatever their number; the large ones bound column by column.
func insertRows(executor SqliteExecutor, statement *insertStatement, rows [][]cell) error {
	if len(rows) == 0 {
		return nil
	}
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
	if len(small) > 0 {
		for _, array := range jsonArrays(small) {
			if err := executor.Run(statement.small, array); err != nil {
				return err
			}
		}
	}
	return insertBound(executor, statement, large)
}

// maxParameters: Durable Objects bind at most 100 parameters per statement.
const maxParameters = 100

// insertBound inserts rows with statement.head, statement.width cells each, in statements of 1, 2, 4, ... rows up to
// maxParameters cells: few statements, and at most a handful of SQL texts per table for prepared statements.
func insertBound(executor SqliteExecutor, statement *insertStatement, rows [][]cell) error {
	width := statement.width
	size := 1
	for size*2*width <= maxParameters {
		size *= 2
	}
	// size only shrinks: halved before a chunk to fit the rows left, it is also what the loop then steps over.
	for start := 0; start < len(rows); start += size {
		for size > len(rows)-start {
			size /= 2
		}
		values := make([]string, size)
		params := make([]SqliteValue, 0, size*width)
		for index := range values {
			values[index] = statement.row
			params = append(params, rows[start+index]...)
		}
		if err := executor.Run(statement.head+" "+strings.Join(values, ", ")+" "+statement.conflict, params...); err != nil {
			return err
		}
	}
	return nil
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
