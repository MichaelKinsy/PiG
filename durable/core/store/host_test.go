// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// testHost is the synchronous SQLite host of ABI section 3 over one database file: it applies commits (each in its own
// transaction, with the single-writer guard checked) and answers reads, counting the rows it returns.
type testHost struct {
	t     testing.TB
	db    *sql.DB
	path  string
	b     *abi.Builder
	table []string
	// rewrite, when set, renames the SQL text below the host, as an adapter between the host and the database does
	// (the Cloudflare Agents SDK's PiHarness prefixes Pi's table names); the host's own checks see the core's text.
	rewrite  func(string) string
	applied  [][]string // the core's statement texts of every commit applied, in order
	rowsRead int
	reads    int
	commits  int
}

func newHost(t testing.TB, path string) *testHost {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "store.sqlite")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	h := &testHost{t: t, db: db, path: path, b: abi.NewBuilder(), table: store.SQL()}
	t.Cleanup(func() { _ = db.Close() })
	return h
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
			out[i] = append([]byte{}, p.Bytes...)
		}
	}
	return out
}

// sql is the text the database runs for the core's statement text.
func (h *testHost) sql(text string) string {
	if h.rewrite == nil {
		return text
	}
	return h.rewrite(text)
}

// apply executes the commits of the current step.
func (h *testHost) apply() {
	h.t.Helper()
	for _, c := range h.b.Step().Commits {
		tx, err := h.db.Begin()
		if err != nil {
			h.t.Fatal(err)
		}
		texts := make([]string, 0, len(c.Stmts))
		for _, s := range c.Stmts {
			text := h.table[s.SQL]
			texts = append(texts, text)
			res, err := tx.Exec(h.sql(text), bind(s.Params)...)
			if err != nil {
				_ = tx.Rollback()
				h.t.Fatalf("commit %d: %v\n%s", c.Seq, err, text)
			}
			if strings.HasPrefix(text, "UPDATE durable_metadata") {
				if n, _ := res.RowsAffected(); n != 1 {
					_ = tx.Rollback()
					h.t.Fatalf("commit %d: single-writer guard changed %d rows", c.Seq, n)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			h.t.Fatal(err)
		}
		h.commits++
		h.applied = append(h.applied, texts)
	}
}

// answer runs the reads of the current step and returns the rows event.
func (h *testHost) answer() []abi.ReadRows {
	h.t.Helper()
	var out []abi.ReadRows
	for _, r := range h.b.Step().Reads {
		rows, err := h.db.Query(h.sql(h.table[r.SQL]), bind(r.Params)...)
		if err != nil {
			h.t.Fatalf("read %q: %v", h.table[r.SQL], err)
		}
		cols, _ := rows.Columns()
		ans := abi.ReadRows{ID: r.ID, Cols: uint8(len(cols))}
		for rows.Next() {
			dest := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range dest {
				ptrs[i] = &dest[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				h.t.Fatal(err)
			}
			row := make([]abi.Value, len(cols))
			for i, v := range dest {
				switch x := v.(type) {
				case nil:
					row[i] = abi.Null()
				case int64:
					row[i] = abi.Int(x)
				case float64:
					row[i] = abi.Float(x)
				case string:
					row[i] = abi.Text(x)
				case []byte:
					row[i] = abi.Blob(append([]byte(nil), x...))
				default:
					h.t.Fatalf("column type %T", v)
				}
			}
			ans.Rows = append(ans.Rows, row)
			h.rowsRead++
		}
		_ = rows.Close()
		h.reads++
		out = append(out, ans)
	}
	return out
}

// run calls f until it stops returning store.ErrPending, answering reads between calls (the core's read deferral), and
// applies the commits f emits. It returns f's last error.
func (h *testHost) run(st *store.Store, f func() error) error {
	h.t.Helper()
	for range 64 {
		h.b.Reset()
		err := f()
		if !errors.Is(err, store.ErrPending) {
			h.apply()
			return err
		}
		for {
			h.apply() // ABI section 3, invariant 3: a step's commits are applied before its reads run
			rows := h.answer()
			h.b.Reset()
			e := st.Rows(h.b, rows)
			if !errors.Is(e, store.ErrPending) {
				if e != nil {
					return e
				}
				h.apply()
				break
			}
		}
	}
	h.t.Fatal("read loop does not converge")
	return nil
}

func (h *testHost) openStore(opts store.Options) (*store.Store, *store.Live, error) {
	h.t.Helper()
	st := store.New(opts)
	var live *store.Live
	err := h.run(st, func() (err error) { live, err = st.Open(h.b); return })
	return st, live, err
}

// close checkpoints the WAL so another process reads the file.
func (h *testHost) close() {
	h.t.Helper()
	if _, err := h.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.Close(); err != nil {
		h.t.Fatal(err)
	}
}
