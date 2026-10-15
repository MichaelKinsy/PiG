// Package storehost is the synchronous SQLite host of ABI section 3 for the store alone: tooling that drives store.Store (the
// benchmark engine, the storage-conformance provider) applies a step's commits and answers its reads with it.
package storehost

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

// Host applies the commits a core step emits, one transaction each, answers its reads, and records every commit in the trace
// when it has one.
type Host struct {
	db    *sql.DB
	trace *contracttest.Writer
	// B is the builder the store writes its steps into.
	B     *abi.Builder
	table []string
}

// Open opens (creating it if needed) the store file at path in WAL mode. trace may be nil.
func Open(path string, trace *contracttest.Writer) (*Host, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &Host{db: db, trace: trace, B: abi.NewBuilder(), table: store.SQL()}, nil
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

// apply runs the commits of the current step. A trace line follows each commit once the store holds it, so a process killed
// after commit k leaves the commit in the store and in the trace.
// RecordType is the record_ids type that owns id, or "" when no committed record does.
func (h *Host) RecordType(id int64) (string, error) {
	var kind string
	err := h.db.QueryRow(`SELECT record_type FROM record_ids WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return kind, err
}

func (h *Host) apply() error {
	for _, c := range h.B.Step().Commits {
		tx, err := h.db.Begin()
		if err != nil {
			return err
		}
		rec := make([]contracttest.Stmt, 0, len(c.Stmts))
		for _, s := range c.Stmts {
			text := h.table[s.SQL]
			args := bind(s.Params)
			res, err := tx.Exec(text, args...)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("commit %d: %w\n%s", c.Seq, err, text)
			}
			if n, _ := res.RowsAffected(); n != 1 && len(text) > 6 && text[:6] == "UPDATE" && contains(text, "durable_metadata") {
				_ = tx.Rollback()
				return fmt.Errorf("commit %d: the single-writer guard changed %d rows", c.Seq, n)
			}
			ps := make([]contracttest.Param, len(args))
			for i, a := range args {
				ps[i] = a
			}
			rec = append(rec, contracttest.Stmt{SQL: text, Params: ps})
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if h.trace != nil {
			if err := h.trace.Commit("s0", c.Seq, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// answer runs the reads of the current step.
func (h *Host) answer() ([]abi.ReadRows, error) {
	var out []abi.ReadRows
	for _, r := range h.B.Step().Reads {
		rows, err := h.db.Query(h.table[r.SQL], bind(r.Params)...)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", h.table[r.SQL], err)
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
				_ = rows.Close()
				return nil, err
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
					_ = rows.Close()
					return nil, fmt.Errorf("column type %T", v)
				}
			}
			ans.Rows = append(ans.Rows, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		_ = rows.Close()
		out = append(out, ans)
	}
	return out, nil
}

// Run calls f until it stops returning store.ErrPending (the core's read deferral: f runs again from the top once the rows
// are in) and applies the commits f emits.
func (h *Host) Run(st *store.Store, f func() error) error {
	for range 1 << 16 {
		h.B.Reset()
		err := f()
		if !errors.Is(err, store.ErrPending) {
			if aerr := h.apply(); aerr != nil {
				return aerr
			}
			return err
		}
		for {
			// ABI section 3, invariant 3: a step's commits are applied before its reads run.
			if aerr := h.apply(); aerr != nil {
				return aerr
			}
			rows, aerr := h.answer()
			if aerr != nil {
				return aerr
			}
			h.B.Reset()
			e := st.Rows(h.B, rows)
			if !errors.Is(e, store.ErrPending) {
				if e != nil {
					return e
				}
				if aerr := h.apply(); aerr != nil {
					return aerr
				}
				break
			}
		}
	}
	return errors.New("the read loop does not converge")
}

// Close checkpoints the WAL and closes the file.
func (h *Host) Close() error {
	if _, err := h.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	return h.db.Close()
}
