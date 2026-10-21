package telemetrysqlite

// Ports packages/chord/test/sqlite/helpers.ts durableObjectStorage: a stand-in for a SQLite-backed Durable Object's
// `ctx.storage` over a native SQLite connection, with its value types ([]byte for BLOB; Exec without bindings may run
// several statements).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	// Registers the pure-Go "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/chord/sqlite/cloudflare"
)

type durableObjectStorage struct{ conn *sql.Conn }

type durableObjectCursor struct {
	rows  []map[string]cloudflare.DurableObjectSqlValue
	index int
}

func (cursor *durableObjectCursor) Next() (map[string]cloudflare.DurableObjectSqlValue, bool, error) {
	if cursor.index >= len(cursor.rows) {
		return nil, false, nil
	}
	cursor.index++
	return cursor.rows[cursor.index-1], true, nil
}

func (cursor *durableObjectCursor) ToArray() ([]map[string]cloudflare.DurableObjectSqlValue, error) {
	remaining := cursor.rows[cursor.index:]
	cursor.index = len(cursor.rows)
	return remaining, nil
}

func newDurableObjectStorage(t *testing.T) *durableObjectStorage {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	check(t, err)
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	check(t, err)
	t.Cleanup(func() { _ = conn.Close(); _ = db.Close() })
	return &durableObjectStorage{conn: conn}
}

func (storage *durableObjectStorage) Sql() cloudflare.DurableObjectSql { return storage }

func (storage *durableObjectStorage) Exec(text string, bindings ...cloudflare.DurableObjectSqlValue) (cloudflare.DurableObjectSqlCursor, error) {
	ctx := context.Background()
	if len(bindings) == 0 && strings.Contains(strings.TrimRight(strings.TrimSpace(text), "; \t\r\n"), ";") {
		_, err := storage.conn.ExecContext(ctx, text)
		return &durableObjectCursor{}, err
	}
	rows, err := storage.conn.QueryContext(ctx, text, bindings...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	found := []map[string]cloudflare.DurableObjectSqlValue{}
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]cloudflare.DurableObjectSqlValue, len(columns))
		for index, column := range columns {
			row[column] = values[index]
		}
		found = append(found, row)
	}
	return &durableObjectCursor{rows: found}, rows.Err()
}

func (storage *durableObjectStorage) Transaction(closure func() error) error {
	ctx := context.Background()
	if _, err := storage.conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	if err := closure(); err != nil {
		_, _ = storage.conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	_, err := storage.conn.ExecContext(ctx, "COMMIT")
	return err
}
