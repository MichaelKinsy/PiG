// Command replayhost is a script-driven stand-in for a native Durable core host: it applies the statements of a recorded trace
// to a SQLite store, one transaction per commit, and writes its own trace with contracttest.Writer. It has no Pi semantics, so
// it cannot recover or decide anything; it exists to prove the native path of the conformance gate end to end (a Go process
// writing the store through modernc SQLite, killed after a commit, a trace of statements only, replayed by the Node tools).
//
//	replayhost --db <store> --trace <out> --script <trace to replay> [--from-seq N]
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	_ "modernc.org/sqlite"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest"
)

func main() {
	db := flag.String("db", "", "store")
	out := flag.String("trace", "", "trace to write")
	script := flag.String("script", "", "trace whose commits to replay")
	from := flag.Int64("from-seq", 0, "skip commits below this seq")
	// The bench options every native candidate is started with; a script-driven host has nothing to do with them.
	for _, name := range []string{"turns", "tools", "variant", "seed", "stream", "partial-ms", "fault", "payload-kb", "label"} {
		flag.String(name, "", "ignored")
	}
	flag.Bool("parallel", false, "ignored")
	flag.Bool("recover", false, "ignored")
	flag.Parse()
	if err := run(*db, *out, *script, *from); err != nil {
		fmt.Fprintln(os.Stderr, "replayhost:", err)
		os.Exit(1)
	}
}

func run(dbPath, outPath, script string, from int64) error {
	lines, err := contracttest.ReadTrace(script)
	if err != nil {
		return err
	}
	texts := map[int]string{}
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	conn.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode = WAL", "PRAGMA synchronous = NORMAL"} {
		if _, err := conn.Exec(q); err != nil {
			return err
		}
	}
	w, err := contracttest.NewWriter(outPath)
	if err != nil {
		return err
	}
	if err := w.Meta("replayhost", "replayhost"); err != nil {
		return err
	}
	if err := w.Store("s0", dbPath); err != nil {
		return err
	}
	for _, l := range lines {
		switch l.T {
		case "sql":
			var q int
			var text string
			_ = json.Unmarshal(l.Fields["q"], &q)
			_ = json.Unmarshal(l.Fields["text"], &text)
			texts[q] = text
		case "commit":
			var seq int64
			_ = json.Unmarshal(l.Fields["seq"], &seq)
			if seq < from {
				continue
			}
			var stmts []struct {
				Q int             `json:"q"`
				P json.RawMessage `json:"p"`
			}
			if err := json.Unmarshal(l.Fields["stmts"], &stmts); err != nil {
				return err
			}
			tx, err := conn.Begin()
			if err != nil {
				return err
			}
			var rec []contracttest.Stmt
			for _, s := range stmts {
				params, err := contracttest.DecodeParams(s.P)
				if err != nil {
					return err
				}
				args := make([]any, len(params))
				for i, p := range params {
					args[i] = p
				}
				if _, err := tx.Exec(texts[s.Q], args...); err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("seq %d: %w", seq, err)
				}
				rec = append(rec, contracttest.Stmt{SQL: texts[s.Q], Params: params})
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			if err := w.Commit("s0", seq, rec); err != nil {
				return err
			}
		}
	}
	return w.Close(0)
}
