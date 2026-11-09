package sqlite

import (
	"fmt"
	"unsafe"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// rowInt reads an INTEGER column.
func rowInt(row SqliteRow, column string) (int64, error) { return chordsqlite.RowInt(row, column) }

// rowText reads a TEXT column.
func rowText(row SqliteRow, column string) (string, error) { return valueText(row[column], column) }

// valueInt reads an INTEGER cell of column, as rowInt does.
func valueInt(value SqliteValue, column string) (int64, error) {
	if number, ok := value.(int64); ok {
		return number, nil
	}
	return chordsqlite.RowInt(SqliteRow{column: value}, column)
}

// valueText reads a TEXT cell of column.
func valueText(value SqliteValue, column string) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	}
	return "", fmt.Errorf("SQLite column %s is not text: %v", column, value)
}

// stringBytes is text's bytes without a copy. The result is for reading (a decoder's input); nothing writes it, and it
// keeps text alive.
func stringBytes(text string) []byte {
	return unsafe.Slice(unsafe.StringData(text), len(text)) //nolint:gosec // G103: a read-only view of text, which it keeps alive; decoders do not write their input
}

// eachRow runs sql with params and passes the values of each result row to fn, in column order; columns names them for
// an executor that has no row reader and builds a map per row, with Get when single (the query is a lookup of at most
// one row) and All otherwise. fn's error ends the read and is returned as it is; the values slice is reused for the
// next row.
func eachRow(executor SqliteExecutor, single bool, sql string, params []SqliteValue, columns []string, fn func(values []SqliteValue) error) error {
	switch typed := executor.(type) {
	case watchedExecutor:
		// fn's failures are Durable's own: only the database's mark the calls as failed.
		var failure error
		err := eachRow(typed.executor, single, sql, params, columns, func(values []SqliteValue) error {
			failure = fn(values)
			return failure
		})
		if failure == nil {
			err = typed.watch(err)
		}
		return err
	case chordsqlite.RowReader:
		return typed.EachRow(sql, params, fn)
	}
	var rows []SqliteRow
	var err error
	if single {
		var row SqliteRow
		if row, err = executor.Get(sql, params...); err == nil && row != nil {
			rows = []SqliteRow{row}
		}
	} else {
		rows, err = executor.All(sql, params...)
	}
	if err != nil {
		return err
	}
	values := make([]SqliteValue, len(columns))
	for _, row := range rows {
		for index, column := range columns {
			values[index] = row[column]
		}
		if err := fn(values); err != nil {
			return err
		}
	}
	return nil
}
