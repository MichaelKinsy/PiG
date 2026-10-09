// SPDX-License-Identifier: MIT

package sqlhost

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	// Registers the pure-Go "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// DB is one dedicated SQLite connection. Only the owner goroutine of a Host uses it, so it takes no lock. Statements
// are prepared once per SQL text, the way the core's sql_table is used: a core statement ID is a stable index.
type DB struct {
	db    *sql.DB
	conn  *sql.Conn
	stmts map[string]*sql.Stmt
	inTx  bool
}

// OpenDB opens path (or ":memory:") as one connection with WAL journaling, the setting the node:sqlite host uses.
func OpenDB(ctx context.Context, path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	d := &DB{db: db, conn: conn, stmts: map[string]*sql.Stmt{}}
	pragmas := "PRAGMA busy_timeout = 5000"
	if path != ":memory:" {
		pragmas += "; PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL"
	}
	if _, err := conn.ExecContext(ctx, pragmas); err != nil {
		_ = d.Close()
		return nil, err
	}
	return d, nil
}

// Close releases the prepared statements and the connection.
func (d *DB) Close() error {
	for _, s := range d.stmts {
		_ = s.Close()
	}
	d.stmts = nil
	err := d.conn.Close()
	if cerr := d.db.Close(); err == nil {
		err = cerr
	}
	return err
}

func (d *DB) prepare(ctx context.Context, text string) (*sql.Stmt, error) {
	if s, ok := d.stmts[text]; ok {
		return s, nil
	}
	s, err := d.conn.PrepareContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("prepare %q: %w", abbreviate(text), err)
	}
	d.stmts[text] = s
	return s, nil
}

func abbreviate(text string) string {
	if len(text) > 80 {
		return text[:80] + "..."
	}
	return text
}

func bind(params []abi.Value) []any {
	out := make([]any, len(params))
	for i, p := range params {
		switch p.Kind {
		case abi.ValueNull:
			out[i] = nil
		case abi.ValueInt:
			out[i] = p.Int
		case abi.ValueFloat:
			out[i] = p.Float
		case abi.ValueText:
			out[i] = string(p.Bytes)
		default:
			// A nil []byte would bind NULL, an empty blob must bind a zero-length BLOB.
			b := p.Bytes
			if b == nil {
				b = []byte{}
			}
			out[i] = b
		}
	}
	return out
}

// Begin starts an immediate transaction: the core's single-writer guard names the one writer, so the lock is taken up front.
func (d *DB) Begin(ctx context.Context) error {
	if _, err := d.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	d.inTx = true
	return nil
}

// Commit ends the transaction.
func (d *DB) Commit(ctx context.Context) error {
	_, err := d.conn.ExecContext(ctx, "COMMIT")
	d.inTx = false
	return err
}

// Rollback abandons the transaction. A rollback that fails leaves the outcome unknown and the caller treats it as fatal.
func (d *DB) Rollback(ctx context.Context) error {
	if !d.inTx {
		return nil
	}
	d.inTx = false
	_, err := d.conn.ExecContext(ctx, "ROLLBACK")
	return err
}

// Run executes one write statement and returns the rows it changed.
func (d *DB) Run(ctx context.Context, text string, params []abi.Value) (int64, error) {
	s, err := d.prepare(ctx, text)
	if err != nil {
		return 0, err
	}
	res, err := s.ExecContext(ctx, bind(params)...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// All runs one query and returns its rows and column count. TEXT columns come back as text, BLOB columns as blobs.
func (d *DB) All(ctx context.Context, text string, params []abi.Value) ([][]abi.Value, int, error) {
	s, err := d.prepare(ctx, text)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.QueryContext(ctx, bind(params)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}
	var out [][]abi.Value
	dest := make([]any, len(cols))
	holders := make([]any, len(cols))
	for i := range dest {
		holders[i] = &dest[i]
	}
	for rows.Next() {
		if err := rows.Scan(holders...); err != nil {
			return nil, 0, err
		}
		row := make([]abi.Value, len(cols))
		for i, v := range dest {
			cell, err := toValue(v)
			if err != nil {
				return nil, 0, err
			}
			row[i] = cell
		}
		out = append(out, row)
	}
	return out, len(cols), rows.Err()
}

func toValue(v any) (abi.Value, error) {
	switch x := v.(type) {
	case nil:
		return abi.Null(), nil
	case int64:
		return abi.Int(x), nil
	case float64:
		return abi.Float(x), nil
	case string:
		return abi.Text(x), nil
	case []byte:
		return abi.Blob(append([]byte(nil), x...)), nil
	case bool:
		if x {
			return abi.Int(1), nil
		}
		return abi.Int(0), nil
	}
	return abi.Value{}, fmt.Errorf("sqlhost: unsupported column type %T", v)
}

// isGuard reports whether a statement is the single-writer guard: the durable_metadata update every core commit carries.
func isGuard(text string) bool { return strings.HasPrefix(text, "UPDATE durable_metadata") }

var errNoStatement = errors.New("statement is not in the core's sql_table")
