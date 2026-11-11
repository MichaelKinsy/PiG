package node

// The driver opens every connection in SQLite's multi-thread mode (SQLITE_OPEN_NOMUTEX, see
// dataSourceName), which is only sound when one goroutine at a time uses a connection. These tests enforce that
// contract on the real driver connections the package opens (NewDatabaseSync and OpenNodeSqliteDatabase, through the
// newConnector seam): a wrapper refuses, and counts, every driver call that starts while another call on the same
// connection is still inside the driver, so a regression is an assertion with a message and not a crash that depends on
// timing. They also check that every connection the package opens asks the driver for multi-thread mode.

import (
	"context"
	"database/sql/driver"
	"errors"
	"math/rand/v2"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
)

// errOverlap is what exclusiveConn returns, without reaching the driver, to a call that starts while another call on
// the same connection is inside the driver.
var errOverlap = errors.New("exclusiveConn: a second goroutine entered the connection while another call was inside the driver")

// exclusiveConn forwards to a real driver connection one call at a time. A call that starts while another is running
// (on the connection, or on a statement or rows of it) is refused with errOverlap and counted.
type exclusiveConn struct {
	real driver.Conn
	// inside is 1 while a call is inside the driver.
	inside atomic.Int32
	// overlaps counts the refused calls; calls counts the calls that reached the driver.
	overlaps atomic.Int64
	calls    atomic.Int64
	// hold, when set, is signalled once a call is inside the driver and then blocks that call until it is closed.
	hold chan struct{}
	held chan struct{}
}

// enter records the start of a driver call: the function that records its end, or errOverlap.
func (conn *exclusiveConn) enter() (func(), error) {
	if !conn.inside.CompareAndSwap(0, 1) {
		conn.overlaps.Add(1)
		return nil, errOverlap
	}
	conn.calls.Add(1)
	if hold := conn.hold; hold != nil {
		select {
		case conn.held <- struct{}{}:
			<-hold
		default:
		}
	}
	return func() { conn.inside.Store(0) }, nil
}

func (conn *exclusiveConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("exclusiveConn: Prepare without a context")
}

func (conn *exclusiveConn) Close() error {
	exit, err := conn.enter()
	if err != nil {
		return err
	}
	defer exit()
	return conn.real.Close()
}

func (conn *exclusiveConn) Begin() (driver.Tx, error) {
	return nil, errors.New("exclusiveConn: Begin is not used")
}

func (conn *exclusiveConn) PrepareContext(ctx context.Context, sqlText string) (driver.Stmt, error) {
	exit, err := conn.enter()
	if err != nil {
		return nil, err
	}
	defer exit()
	stmt, err := conn.real.(driver.ConnPrepareContext).PrepareContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	return &exclusiveStmt{conn: conn, real: stmt}, nil
}

func (conn *exclusiveConn) ExecContext(ctx context.Context, sqlText string, args []driver.NamedValue) (driver.Result, error) {
	exit, err := conn.enter()
	if err != nil {
		return nil, err
	}
	defer exit()
	return conn.real.(driver.ExecerContext).ExecContext(ctx, sqlText, args)
}

type exclusiveStmt struct {
	conn *exclusiveConn
	real driver.Stmt
}

func (stmt *exclusiveStmt) Close() error {
	exit, err := stmt.conn.enter()
	if err != nil {
		return err
	}
	defer exit()
	return stmt.real.Close()
}

func (stmt *exclusiveStmt) NumInput() int { return stmt.real.NumInput() }

func (stmt *exclusiveStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("exclusiveStmt: Exec without a context")
}

func (stmt *exclusiveStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("exclusiveStmt: Query without a context")
}

func (stmt *exclusiveStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	exit, err := stmt.conn.enter()
	if err != nil {
		return nil, err
	}
	defer exit()
	return stmt.real.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (stmt *exclusiveStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	exit, err := stmt.conn.enter()
	if err != nil {
		return nil, err
	}
	defer exit()
	rows, err := stmt.real.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		return nil, err
	}
	return &exclusiveRows{conn: stmt.conn, real: rows}, nil
}

type exclusiveRows struct {
	conn *exclusiveConn
	real driver.Rows
}

func (rows *exclusiveRows) Columns() []string { return rows.real.Columns() }

func (rows *exclusiveRows) Close() error {
	exit, err := rows.conn.enter()
	if err != nil {
		return err
	}
	defer exit()
	return rows.real.Close()
}

func (rows *exclusiveRows) Next(dest []driver.Value) error {
	exit, err := rows.conn.enter()
	if err != nil {
		return err
	}
	defer exit()
	return rows.real.Next(dest)
}

// exclusiveConnector wraps the connections of a real connector in exclusiveConn and records the data source name.
type exclusiveConnector struct {
	real  driver.Connector
	watch *watch
}

func (connector *exclusiveConnector) Connect(ctx context.Context) (driver.Conn, error) {
	real, err := connector.real.Connect(ctx)
	if err != nil {
		return nil, err
	}
	conn := &exclusiveConn{real: real}
	connector.watch.mu.Lock()
	connector.watch.conns = append(connector.watch.conns, conn)
	connector.watch.mu.Unlock()
	return conn, nil
}

func (connector *exclusiveConnector) Driver() driver.Driver { return connector.real.Driver() }

// watch is what a test sees of the connections the package opened while it ran.
type watch struct {
	mu    sync.Mutex
	dsns  []string
	conns []*exclusiveConn
}

// watchConnections makes every connection NewDatabaseSync opens, until the test ends, an exclusiveConn.
func watchConnections(t *testing.T) *watch {
	t.Helper()
	seen := &watch{}
	opened := newConnector
	newConnector = func(dsn string) (driver.Connector, error) {
		connector, err := opened(dsn)
		if err != nil {
			return nil, err
		}
		seen.mu.Lock()
		seen.dsns = append(seen.dsns, dsn)
		seen.mu.Unlock()
		return &exclusiveConnector{real: connector, watch: seen}, nil
	}
	t.Cleanup(func() { newConnector = opened })
	return seen
}

// totals returns the refused and the forwarded calls of every connection opened so far.
func (seen *watch) totals() (overlaps, calls int64) {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	for _, conn := range seen.conns {
		overlaps += conn.overlaps.Load()
		calls += conn.calls.Load()
	}
	return overlaps, calls
}

// Every connection the package opens asks the driver for multi-thread mode: NewDatabaseSync and OpenNodeSqliteDatabase,
// read-write and read-only (the connection OpenNodeSqliteDatabase opens read-only is NewDatabaseSync's), for a plain
// path, a path that needs a file: URI, and :memory:.
func TestEveryOpenedConnectionIsMultiThread(t *testing.T) {
	seen := watchConnections(t)
	dir := t.TempDir()
	plain, odd, readOnlyFile := filepath.Join(dir, "plain.db"), filepath.Join(dir, "odd?name#.db"), filepath.Join(dir, "ro.db")
	for _, path := range []string{plain, odd, readOnlyFile} {
		database, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
		mustDo(t, err)
		mustDo(t, database.Close())
	}
	var opened []func() error
	add := func(closer func() error, err error) {
		t.Helper()
		mustDo(t, err)
		opened = append(opened, closer)
	}
	for _, path := range []string{":memory:", plain, odd} {
		raw, err := NewDatabaseSync(path, DatabaseSyncOptions{})
		add(raw.Close, err)
	}
	for _, path := range []string{plain, odd, readOnlyFile} {
		raw, err := NewDatabaseSync(path, DatabaseSyncOptions{ReadOnly: true})
		add(raw.Close, err)
	}
	for _, closer := range opened {
		mustDo(t, closer())
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if len(seen.dsns) < 9 {
		t.Fatalf("only %d connections observed; the seam is not on the open path", len(seen.dsns))
	}
	for _, dsn := range seen.dsns {
		_, query, _ := strings.Cut(dsn, "?")
		values, err := url.ParseQuery(query)
		if err != nil || len(values["_threading_mode"]) != 1 || values["_threading_mode"][0] != "multi-thread" {
			t.Errorf("opened with %q: want _threading_mode=multi-thread exactly once", dsn)
		}
	}
}

// workload runs one goroutine's share of mixed operations through the adapter; reads only when readOnly (a read-only
// connection cannot BEGIN IMMEDIATE).
func workload(adapter *NodeSqliteDatabase, worker, rounds int, readOnly bool) (done int, err error) {
	for round := range rounds {
		var opErr error
		switch step := (worker + round) % 5; {
		case step == 0 && !readOnly:
			opErr = adapter.Run("INSERT INTO shared (value) VALUES (?)", int64(round))
		case step == 1 || step == 0:
			_, opErr = adapter.Get("SELECT count(*) AS n FROM shared")
		case step == 2:
			_, opErr = adapter.All("SELECT value FROM shared WHERE value <= ? ORDER BY value LIMIT 20", int64(round))
		case step == 3:
			opErr = adapter.EachRow("SELECT value FROM shared LIMIT 5", nil, func([]chordsqlite.SqliteValue) error { return nil })
		case readOnly:
			_, opErr = adapter.All("SELECT value FROM shared LIMIT 3")
		default:
			opErr = adapter.Transaction(func(handle chordsqlite.SqliteExecutor) error {
				if err := handle.Run("INSERT INTO shared (value) VALUES (?)", int64(round)); err != nil {
					return err
				}
				_, err := handle.All("SELECT value FROM shared LIMIT 3")
				return err
			})
		}
		if opErr != nil {
			return done, opErr
		}
		done++
	}
	return done, nil
}

// Every operation of a NodeSqliteDatabase, whichever goroutine calls it, reaches the driver one at a time: the serial
// queue holds the connection for each operation and for a whole transaction.
func TestNodeSqliteDatabaseUsesOneConnectionFromOneGoroutineAtATime(t *testing.T) {
	seen := watchConnections(t)
	adapter := mustOpenMemory(t)
	mustDo(t, adapter.Exec("CREATE TABLE shared (value INTEGER)"))
	const goroutines, rounds = 16, 60
	var group sync.WaitGroup
	errs := make(chan error, goroutines)
	for worker := range goroutines {
		group.Go(func() {
			if _, err := workload(adapter, worker, rounds, false); err != nil {
				errs <- err
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	mustDo(t, adapter.Close())
	overlaps, calls := seen.totals()
	if overlaps != 0 {
		t.Fatalf("%d driver calls were refused: they started while another call on the same connection was running", overlaps)
	}
	if calls < goroutines*rounds {
		t.Fatalf("only %d driver calls observed; the wrapper is not on the path", calls)
	}
}

// Two file-backed WAL connections of one database (a writer and a read-only reader), eight goroutines on each, and
// Close called from yet another goroutine after a random delay while the operations run: nothing reaches a connection
// twice at once, and an operation that loses the race to Close fails with an error instead of running.
func TestCloseAmongConcurrentOperationsKeepsOneGoroutineInTheConnection(t *testing.T) {
	seen := watchConnections(t)
	succeeded := 0
	for iteration := range 20 {
		path := filepath.Join(t.TempDir(), "shared.db")
		writer, err := OpenNodeSqliteDatabase(path, NodeSqliteDatabaseOptions{})
		mustDo(t, err)
		mustDo(t, writer.Exec("CREATE TABLE shared (value INTEGER)"))
		readOnly, err := NewDatabaseSync(path, DatabaseSyncOptions{TimeoutMs: 5000, ReadOnly: true})
		mustDo(t, err)
		reader := newNodeSqliteDatabase(readOnly)
		var group sync.WaitGroup
		var done atomic.Int64
		for worker := range 8 {
			group.Go(func() {
				count, _ := workload(writer, worker, 40, false) // an operation after Close fails: expected
				done.Add(int64(count))
			})
			group.Go(func() {
				count, _ := workload(reader, worker, 40, true)
				done.Add(int64(count))
			})
		}
		group.Go(func() {
			time.Sleep(time.Duration(rand.IntN(20)) * time.Millisecond)
			_ = writer.Close()
		})
		group.Go(func() {
			time.Sleep(time.Duration(rand.IntN(20)) * time.Millisecond)
			_ = reader.Close()
		})
		group.Wait()
		_ = writer.Close()
		_ = reader.Close()
		succeeded += int(done.Load())
		if overlaps, _ := seen.totals(); overlaps != 0 {
			t.Fatalf("iteration %d: %d driver calls were refused: they started while another call on the same connection was running", iteration, overlaps)
		}
	}
	if _, calls := seen.totals(); calls == 0 || succeeded == 0 {
		t.Fatalf("no operation ran (%d driver calls, %d operations); the test is not exercising the connections", calls, succeeded)
	}
}

// The detector itself: a call made while another is inside the driver (the misuse multi-thread mode does not
// tolerate, here a raw DatabaseSync used from two goroutines without the serial queue) is refused with errOverlap
// and counted, and never reaches the driver.
func TestExclusiveConnRefusesOverlappingUse(t *testing.T) {
	seen := watchConnections(t)
	database, err := NewDatabaseSync(":memory:", DatabaseSyncOptions{})
	mustDo(t, err)
	conn := seen.conns[0]
	hold := make(chan struct{})
	conn.hold, conn.held = hold, make(chan struct{})
	first := startErr(func() error { return database.Exec("SELECT 1") })
	<-conn.held // the first call is now inside the driver
	conn.hold = nil
	callsBefore := conn.calls.Load()
	_, err = startErr(func() error { return database.Exec("SELECT 2") }).await()
	if !errors.Is(err, errOverlap) {
		t.Fatalf("a second call made while the first was inside the driver returned %v, want errOverlap", err)
	}
	if conn.overlaps.Load() != 1 || conn.calls.Load() != callsBefore {
		t.Fatalf("overlaps = %d, calls = %d (was %d): the refused call must be counted and not forwarded", conn.overlaps.Load(), conn.calls.Load(), callsBefore)
	}
	close(hold)
	_, err = first.await()
	mustDo(t, err)
}
