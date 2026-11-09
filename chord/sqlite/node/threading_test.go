package node

// pig additive: the driver opens every connection in SQLite's multi-thread mode (SQLITE_OPEN_NOMUTEX, see
// dataSourceName), which is only sound when one goroutine at a time uses a connection. These tests enforce that
// contract on the real driver connection: a wrapper observes every call into the driver and fails on overlap.

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/internal/third_party/sqlite"
)

// exclusiveConn forwards to a real driver connection and counts the calls that start while another call on the same
// connection (or on a statement or rows of it) has not returned.
type exclusiveConn struct {
	real driver.Conn
	// inside is the number of driver calls currently running on the connection.
	inside atomic.Int32
	// overlaps counts the calls that started while another one was running; calls counts all calls.
	overlaps atomic.Int64
	calls    atomic.Int64
	// hold, when set, is signalled once a call is inside the driver and then blocks that call until it is closed.
	hold chan struct{}
	held chan struct{}
}

// enter records the start of a driver call and returns the function that records its end.
func (conn *exclusiveConn) enter() func() {
	conn.calls.Add(1)
	if conn.inside.Add(1) != 1 {
		conn.overlaps.Add(1)
	}
	if hold := conn.hold; hold != nil {
		select {
		case conn.held <- struct{}{}:
			<-hold
		default:
		}
	}
	return func() { conn.inside.Add(-1) }
}

func (conn *exclusiveConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("exclusiveConn: Prepare without a context")
}

func (conn *exclusiveConn) Close() error {
	defer conn.enter()()
	return conn.real.Close()
}

func (conn *exclusiveConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("exclusiveConn: Begin is not used")
}

func (conn *exclusiveConn) PrepareContext(ctx context.Context, sqlText string) (driver.Stmt, error) {
	defer conn.enter()()
	stmt, err := conn.real.(driver.ConnPrepareContext).PrepareContext(ctx, sqlText)
	if err != nil {
		return nil, err
	}
	return &exclusiveStmt{conn: conn, real: stmt}, nil
}

func (conn *exclusiveConn) ExecContext(ctx context.Context, sqlText string, args []driver.NamedValue) (driver.Result, error) {
	defer conn.enter()()
	return conn.real.(driver.ExecerContext).ExecContext(ctx, sqlText, args)
}

type exclusiveStmt struct {
	conn *exclusiveConn
	real driver.Stmt
}

func (stmt *exclusiveStmt) Close() error {
	defer stmt.conn.enter()()
	return stmt.real.Close()
}

func (stmt *exclusiveStmt) NumInput() int { return stmt.real.NumInput() }

func (stmt *exclusiveStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, fmt.Errorf("exclusiveStmt: Exec without a context")
}

func (stmt *exclusiveStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, fmt.Errorf("exclusiveStmt: Query without a context")
}

func (stmt *exclusiveStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	defer stmt.conn.enter()()
	return stmt.real.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (stmt *exclusiveStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	defer stmt.conn.enter()()
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
	defer rows.conn.enter()()
	return rows.real.Close()
}

func (rows *exclusiveRows) Next(dest []driver.Value) error {
	defer rows.conn.enter()()
	return rows.real.Next(dest)
}

// openExclusive opens a driver connection the way NewDatabaseSync does (dataSourceName: multi-thread mode) and
// wraps it.
func openExclusive(t *testing.T) (*DatabaseSync, *exclusiveConn) {
	t.Helper()
	connector, err := sqlite.NewConnector(dataSourceName(":memory:", false))
	if err != nil {
		t.Fatal(err)
	}
	real, err := connector.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	conn := &exclusiveConn{real: real}
	return &DatabaseSync{conn: conn}, conn
}

// Every operation of a NodeSqliteDatabase, whichever goroutine calls it, reaches the driver one at a time: the serial
// queue holds the connection for each operation and for a whole transaction, Close included.
func TestNodeSqliteDatabaseUsesOneConnectionFromOneGoroutineAtATime(t *testing.T) {
	database, conn := openExclusive(t)
	adapter := NewNodeSqliteDatabase(database)
	mustDo(t, adapter.Exec("CREATE TABLE shared (value INTEGER)"))

	const goroutines, rounds = 16, 60
	var group sync.WaitGroup
	errs := make(chan error, goroutines)
	for worker := range goroutines {
		group.Go(func() {
			for round := range rounds {
				var err error
				switch (worker + round) % 5 {
				case 0:
					err = adapter.Run("INSERT INTO shared (value) VALUES (?)", int64(round))
				case 1:
					_, err = adapter.Get("SELECT count(*) AS n FROM shared")
				case 2:
					_, err = adapter.All("SELECT value FROM shared WHERE value <= ? ORDER BY value LIMIT 20", int64(round))
				case 3:
					err = adapter.EachRow("SELECT value FROM shared LIMIT 5", nil, func([]chordsqlite.SqliteValue) error { return nil })
				default:
					err = adapter.Transaction(func(handle chordsqlite.SqliteExecutor) error {
						if err := handle.Run("INSERT INTO shared (value) VALUES (?)", int64(round)); err != nil {
							return err
						}
						_, err := handle.All("SELECT value FROM shared LIMIT 3")
						return err
					})
				}
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	mustDo(t, adapter.Close())
	if overlaps := conn.overlaps.Load(); overlaps != 0 {
		t.Fatalf("%d of %d driver calls started while another call on the same connection was running", overlaps, conn.calls.Load())
	}
	if calls := conn.calls.Load(); calls < goroutines*rounds {
		t.Fatalf("only %d driver calls observed; the wrapper is not on the path", calls)
	}
}

// The detector itself: calls made without the serial queue (a raw DatabaseSync used from two goroutines, the misuse
// multi-thread mode does not tolerate) are counted as overlaps, so a queue that stopped serialising would fail the
// test above.
func TestExclusiveConnDetectsOverlappingUse(t *testing.T) {
	database, conn := openExclusive(t)
	hold := make(chan struct{})
	conn.hold, conn.held = hold, make(chan struct{})
	first := startErr(func() error { return database.Exec("SELECT 1") })
	<-conn.held // the first call is now inside the driver
	conn.hold = nil
	second := startErr(func() error { return database.Exec("SELECT 2") })
	_, err := second.await()
	mustDo(t, err)
	close(hold)
	_, err = first.await()
	mustDo(t, err)
	if conn.overlaps.Load() == 0 {
		t.Fatal("a second call made while the first was inside the driver was not detected")
	}
}

// Every data source name the package builds selects multi-thread mode, whatever the path or mode.
func TestDataSourceNameSelectsMultiThreadMode(t *testing.T) {
	for _, test := range []struct {
		path     string
		readOnly bool
	}{
		{"/tmp/plain.db", false},
		{":memory:", false},
		{"/tmp/odd?name#.db", false},
		{"/tmp/plain.db", true},
	} {
		name := dataSourceName(test.path, test.readOnly)
		if !strings.Contains(name, "_threading_mode=multi-thread") {
			t.Errorf("dataSourceName(%q, %v) = %q, want _threading_mode=multi-thread", test.path, test.readOnly, name)
		}
		if test.readOnly && !strings.Contains(name, "mode=ro") {
			t.Errorf("dataSourceName(%q, true) = %q lost mode=ro", test.path, name)
		}
	}
}
