package main

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

type stat struct {
	n  int
	ns time.Duration
}

var dbgMu sync.Mutex
var dbgStats = map[string]*stat{}

func dbgRecord(sql string, d time.Duration) {
	if len(sql) > 90 {
		sql = sql[:90]
	}
	dbgMu.Lock()
	s := dbgStats[sql]
	if s == nil {
		s = &stat{}
		dbgStats[sql] = s
	}
	s.n++
	s.ns += d
	dbgMu.Unlock()
}

func dbgDump() {
	type kv struct {
		k string
		v *stat
	}
	var all []kv
	for k, v := range dbgStats {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v.ns > all[j].v.ns })
	for _, e := range all[:min(25, len(all))] {
		fmt.Fprintf(os.Stderr, "%7d %9.1fms %7.1fus  %s\n", e.v.n, float64(e.v.ns)/1e6, float64(e.v.ns)/1e3/float64(e.v.n), e.k)
	}
}

type dbgExec struct{ inner sqlite.SqliteExecutor }

func (d dbgExec) Exec(q string) error {
	t := time.Now()
	err := d.inner.Exec(q)
	dbgRecord(q, time.Since(t))
	return err
}
func (d dbgExec) Run(q string, p ...sqlite.SqliteValue) error {
	t := time.Now()
	err := d.inner.Run(q, p...)
	dbgRecord(q, time.Since(t))
	return err
}
func (d dbgExec) Get(q string, p ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	t := time.Now()
	r, err := d.inner.Get(q, p...)
	dbgRecord(q, time.Since(t))
	return r, err
}
func (d dbgExec) All(q string, p ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	t := time.Now()
	r, err := d.inner.All(q, p...)
	dbgRecord(q, time.Since(t))
	return r, err
}

type dbgDB struct {
	dbgExec
	db sqlite.SqliteDatabase
}

func (d dbgDB) Transaction(cb func(sqlite.SqliteExecutor) error) error {
	t := time.Now()
	tl("tx begin")
	err := d.db.Transaction(func(tx sqlite.SqliteExecutor) error { return cb(dbgExec{tx}) })
	tl("tx end")
	dbgRecord("TRANSACTION(total)", time.Since(t))
	return err
}
func (d dbgDB) Close() error { return d.db.Close() }

type tevent struct {
	name string
	at   time.Time
}

var tlMu sync.Mutex
var timeline []tevent

func tl(name string) {
	tlMu.Lock()
	timeline = append(timeline, tevent{name, time.Now()})
	tlMu.Unlock()
}

func tlDump(from time.Time, n int) {
	tlMu.Lock()
	defer tlMu.Unlock()
	last := from
	for _, e := range timeline {
		if e.at.Before(from) {
			continue
		}
		if n--; n < 0 {
			break
		}
		fmt.Fprintf(os.Stderr, "%9.2fms +%7.2fms %s\n", float64(e.at.Sub(from).Microseconds())/1000, float64(e.at.Sub(last).Microseconds())/1000, e.name)
		last = e.at
	}
}

// sqlCounts are the statements the storage issues, counted as the benchmark's profile counts them. Rows read is what
// providers such as Durable Objects bill.
type sqlCounts struct {
	Statements   int64 `json:"statements"`
	Reads        int64 `json:"reads"`
	Writes       int64 `json:"writes"`
	RowsRead     int64 `json:"rowsRead"`
	Transactions int64 `json:"transactions"`
}

var counts sqlCounts

func resetCounts() { counts = sqlCounts{} }

type countingExec struct{ inner sqlite.SqliteExecutor }

func (c countingExec) Exec(q string) error {
	atomic.AddInt64(&counts.Statements, 1)
	atomic.AddInt64(&counts.Writes, 1)
	return c.inner.Exec(q)
}
func (c countingExec) Run(q string, p ...sqlite.SqliteValue) error {
	atomic.AddInt64(&counts.Statements, 1)
	atomic.AddInt64(&counts.Writes, 1)
	return c.inner.Run(q, p...)
}
func (c countingExec) Get(q string, p ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	atomic.AddInt64(&counts.Statements, 1)
	atomic.AddInt64(&counts.Reads, 1)
	row, err := c.inner.Get(q, p...)
	if row != nil {
		atomic.AddInt64(&counts.RowsRead, 1)
	}
	return row, err
}
func (c countingExec) All(q string, p ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	atomic.AddInt64(&counts.Statements, 1)
	atomic.AddInt64(&counts.Reads, 1)
	rows, err := c.inner.All(q, p...)
	atomic.AddInt64(&counts.RowsRead, int64(len(rows)))
	return rows, err
}

// EachText forwards to the database's row reader, counting the rows it hands over.
func (c countingExec) EachText(q string, p []sqlite.SqliteValue, fn func([]byte) error) error {
	atomic.AddInt64(&counts.Statements, 1)
	atomic.AddInt64(&counts.Reads, 1)
	return c.inner.(sqlite.TextReader).EachText(q, p, func(text []byte) error {
		atomic.AddInt64(&counts.RowsRead, 1)
		return fn(text)
	})
}

type countingDB struct {
	countingExec
	db sqlite.SqliteDatabase
}

func (c countingDB) Transaction(cb func(sqlite.SqliteExecutor) error) error {
	atomic.AddInt64(&counts.Transactions, 1)
	return c.db.Transaction(func(tx sqlite.SqliteExecutor) error { return cb(countingExec{tx}) })
}
func (c countingDB) Close() error { return c.db.Close() }

// capture records every write statement of the measured turns with its parameters, for replaying against a driver.
var captureWrites []captured

type captured struct {
	Tx  string `json:"tx,omitempty"`
	SQL string `json:"sql,omitempty"`
	//portlint:allow emptydrop a debug capture of SQL statements for the bench, not a Pi wire shape
	Params []any `json:"params,omitempty"`
}

type captureExec struct{ inner sqlite.SqliteExecutor }

func (c captureExec) Exec(q string) error {
	captureWrites = append(captureWrites, captured{SQL: q})
	return c.inner.Exec(q)
}
func (c captureExec) Run(q string, p ...sqlite.SqliteValue) error {
	captureWrites = append(captureWrites, captured{SQL: q, Params: p})
	return c.inner.Run(q, p...)
}
func (c captureExec) Get(q string, p ...sqlite.SqliteValue) (sqlite.SqliteRow, error) {
	return c.inner.Get(q, p...)
}
func (c captureExec) All(q string, p ...sqlite.SqliteValue) ([]sqlite.SqliteRow, error) {
	return c.inner.All(q, p...)
}

type captureDB struct {
	captureExec
	db sqlite.SqliteDatabase
}

func (c captureDB) Transaction(cb func(sqlite.SqliteExecutor) error) error {
	captureWrites = append(captureWrites, captured{Tx: "begin"})
	err := c.db.Transaction(func(tx sqlite.SqliteExecutor) error { return cb(captureExec{tx}) })
	captureWrites = append(captureWrites, captured{Tx: "end"})
	return err
}
func (c captureDB) Close() error { return c.db.Close() }
