// Package node adapts a native SQLite connection to the portable durable SQLite facade. It is the Go counterpart of
// Pi's node:sqlite adapter and the only durable storage package that links a SQLite driver (modernc.org/sqlite, pure
// Go, CGO-free).
package node

// Ports packages/durable/src/storage/sqlite/node.ts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	// Registers the pure-Go "sqlite" database/sql driver.
	_ "modernc.org/sqlite"

	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// DatabaseSyncOptions configures one native SQLite connection.
type DatabaseSyncOptions struct {
	// TimeoutMs is how long SQLite waits for a competing file lock; zero does not wait.
	TimeoutMs int
	// ReadOnly opens an existing database without write access.
	ReadOnly bool
}

var errDatabaseNotOpen = errors.New("database is not open")

// DatabaseSync is one native SQLite connection whose operations run on the caller's goroutine, the Go counterpart of
// node:sqlite's DatabaseSync.
type DatabaseSync struct {
	mu   sync.Mutex
	db   *sql.DB
	conn *sql.Conn
}

// NewDatabaseSync opens path (or ":memory:") as one dedicated connection.
func NewDatabaseSync(path string, options DatabaseSyncOptions) (*DatabaseSync, error) {
	db, err := sql.Open("sqlite", dataSourceName(path, options.ReadOnly))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	database := &DatabaseSync{db: db, conn: conn}
	if _, err := conn.ExecContext(
		context.Background(), fmt.Sprintf("PRAGMA busy_timeout = %d", max(options.TimeoutMs, 0)),
	); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// dataSourceName keeps a plain path unchanged and uses a file: URI only when the path would otherwise be read as
// driver query parameters or when read-only mode is requested.
func dataSourceName(path string, readOnly bool) string {
	if !readOnly && !strings.ContainsAny(path, "?#") {
		return path
	}
	location := url.URL{Scheme: "file", Opaque: (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()}
	if readOnly {
		location.RawQuery = "mode=ro"
	}
	return location.String()
}

func (database *DatabaseSync) connection() (*sql.Conn, error) {
	database.mu.Lock()
	defer database.mu.Unlock()
	if database.conn == nil {
		return nil, errDatabaseNotOpen
	}
	return database.conn, nil
}

// Exec runs SQL text without bindings; it may contain several statements.
func (database *DatabaseSync) Exec(sqlText string) error {
	conn, err := database.connection()
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(context.Background(), sqlText)
	return err
}

// Prepare compiles one statement.
func (database *DatabaseSync) Prepare(sqlText string) (*StatementSync, error) {
	conn, err := database.connection()
	if err != nil {
		return nil, err
	}
	statement, err := conn.PrepareContext(context.Background(), sqlText)
	if err != nil {
		return nil, err
	}
	return &StatementSync{database: database, statement: statement}, nil
}

// Close closes the connection; later operations fail.
func (database *DatabaseSync) Close() error {
	database.mu.Lock()
	conn := database.conn
	database.conn = nil
	database.mu.Unlock()
	if conn == nil {
		return errDatabaseNotOpen
	}
	return errors.Join(conn.Close(), database.db.Close())
}

// StatementSync is one prepared statement of a DatabaseSync.
type StatementSync struct {
	database  *DatabaseSync
	statement *sql.Stmt
}

func (statement *StatementSync) open() error {
	_, err := statement.database.connection()
	return err
}

// Run executes the statement with positional bindings.
func (statement *StatementSync) Run(params ...any) error {
	if err := statement.open(); err != nil {
		return err
	}
	_, err := statement.statement.ExecContext(context.Background(), params...)
	return err
}

// Get returns the first result row, or nil when there is none.
func (statement *StatementSync) Get(params ...any) (map[string]any, error) {
	rows, err := statement.query(params, 1)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// All returns every result row.
func (statement *StatementSync) All(params ...any) ([]map[string]any, error) {
	return statement.query(params, -1)
}

func (statement *StatementSync) query(params []any, limit int) (result []map[string]any, err error) {
	if err := statement.open(); err != nil {
		return nil, err
	}
	rows, err := statement.statement.QueryContext(context.Background(), params...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result = []map[string]any{}
	for (limit < 0 || len(result) < limit) && rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for index, column := range columns {
			row[column] = values[index]
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (statement *StatementSync) close() error { return statement.statement.Close() }

// Connection is the native connection surface NodeSqliteDatabase drives.
type Connection interface {
	Exec(sql string) error
	Prepare(sql string) (*StatementSync, error)
	Close() error
}

// serialOperationQueue runs operations in call order. An operation starts immediately when nothing is running or
// waiting; otherwise it waits for everything before it. A transaction holds the queue until it settles.
type serialOperationQueue struct {
	mu      sync.Mutex
	tail    chan struct{}
	waiting int
}

func newSerialOperationQueue() *serialOperationQueue {
	ready := make(chan struct{})
	close(ready)
	return &serialOperationQueue{tail: ready}
}

// acquire takes the next turn and blocks until every earlier turn has been released.
func (queue *serialOperationQueue) acquire() (release func()) {
	queue.mu.Lock()
	previous := queue.tail
	turn := make(chan struct{})
	queue.tail = turn
	queue.waiting++
	queue.mu.Unlock()
	<-previous
	queue.mu.Lock()
	queue.waiting--
	queue.mu.Unlock()
	return func() { close(turn) }
}

// queued reports how many turns wait behind the running one.
func (queue *serialOperationQueue) queued() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.waiting
}

// nodeSqliteExecutor executes SQL on one connection. Prepared statements are cached per connection by SQL text, so the
// database and its transaction handles share them across transactions.
type nodeSqliteExecutor struct {
	database   Connection
	statements *statementCache
	// runOperation brackets one operation: the database queues it, a transaction handle checks that it is active.
	runOperation func(operation func() error) error
}

type statementCache struct {
	mu         sync.Mutex
	statements map[string]*StatementSync
}

func (executor *nodeSqliteExecutor) statement(sqlText string) (*StatementSync, error) {
	cache := executor.statements
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if statement := cache.statements[sqlText]; statement != nil {
		return statement, nil
	}
	statement, err := executor.database.Prepare(sqlText)
	if err != nil {
		return nil, err
	}
	cache.statements[sqlText] = statement
	return statement, nil
}

func (executor *nodeSqliteExecutor) Exec(sqlText string) error {
	return executor.runOperation(func() error { return executor.database.Exec(sqlText) })
}

func (executor *nodeSqliteExecutor) Run(sqlText string, params ...durablesqlite.SqliteValue) error {
	return executor.runOperation(func() error {
		statement, err := executor.statement(sqlText)
		if err != nil {
			return err
		}
		return statement.Run(params...)
	})
}

func (executor *nodeSqliteExecutor) Get(
	sqlText string, params ...durablesqlite.SqliteValue,
) (row durablesqlite.SqliteRow, err error) {
	err = executor.runOperation(func() error {
		statement, prepareErr := executor.statement(sqlText)
		if prepareErr != nil {
			return prepareErr
		}
		row, prepareErr = statement.Get(params...)
		return prepareErr
	})
	return row, err
}

func (executor *nodeSqliteExecutor) All(
	sqlText string, params ...durablesqlite.SqliteValue,
) (rows []durablesqlite.SqliteRow, err error) {
	err = executor.runOperation(func() error {
		statement, prepareErr := executor.statement(sqlText)
		if prepareErr != nil {
			return prepareErr
		}
		rows, prepareErr = statement.All(params...)
		return prepareErr
	})
	return rows, err
}

var errTransactionInactive = errors.New("SQLite transaction handle is no longer active")

// AggregateError reports a transaction callback failure together with the rollback failure that followed it. It
// deliberately does not unwrap: the callback error no longer implies that the transaction rolled back.
type AggregateError struct {
	Errors  []error
	Message string
}

func (aggregate *AggregateError) Error() string { return aggregate.Message }

// NodeSqliteDatabase is the SqliteDatabase adapter backed by a native connection.
type NodeSqliteDatabase struct {
	nodeSqliteExecutor
	access *serialOperationQueue
	closed bool
}

// NewNodeSqliteDatabase adapts database; the adapter owns it from now on.
func NewNodeSqliteDatabase(database Connection) *NodeSqliteDatabase {
	adapter := &NodeSqliteDatabase{access: newSerialOperationQueue()}
	adapter.nodeSqliteExecutor = nodeSqliteExecutor{
		database:     database,
		statements:   &statementCache{statements: map[string]*StatementSync{}},
		runOperation: adapter.run,
	}
	return adapter
}

func (adapter *NodeSqliteDatabase) run(operation func() error) error {
	release := adapter.access.acquire()
	defer release()
	return operation()
}

// Transaction runs callback inside BEGIN IMMEDIATE ... COMMIT and rolls back when it fails or panics.
func (adapter *NodeSqliteDatabase) Transaction(
	callback func(transaction durablesqlite.SqliteExecutor) error,
) (err error) {
	release := adapter.access.acquire()
	defer release()
	if err := adapter.database.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	var active atomic.Bool
	active.Store(true)
	handle := &nodeSqliteExecutor{
		database:   adapter.database,
		statements: adapter.statements,
		runOperation: func(operation func() error) error {
			if !active.Load() {
				return errTransactionInactive
			}
			return operation()
		},
	}
	settled := false
	defer func() {
		if settled {
			return
		}
		// The callback panicked: roll back before the panic continues.
		active.Store(false)
		_ = adapter.database.Exec("ROLLBACK")
	}()
	err = callback(handle)
	active.Store(false)
	if err == nil {
		err = adapter.database.Exec("COMMIT")
	}
	settled = true
	if err == nil {
		return nil
	}
	if rollbackErr := adapter.database.Exec("ROLLBACK"); rollbackErr != nil {
		return &AggregateError{
			Errors:  []error{err, rollbackErr},
			Message: "SQLite transaction failed and rollback failed",
		}
	}
	return err
}

// Close truncates the WAL and closes the connection. Repeated calls do nothing.
func (adapter *NodeSqliteDatabase) Close() error {
	release := adapter.access.acquire()
	defer release()
	if adapter.closed {
		return nil
	}
	adapter.closed = true
	adapter.statements.mu.Lock()
	var errs []error
	for sqlText, statement := range adapter.statements.statements {
		errs = append(errs, statement.close())
		delete(adapter.statements.statements, sqlText)
	}
	adapter.statements.mu.Unlock()
	checkpointErr := adapter.database.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	// Pi closes in a finally block, so a close failure replaces the checkpoint failure.
	if closeErr := adapter.database.Close(); closeErr != nil {
		return closeErr
	}
	if checkpointErr != nil {
		return checkpointErr
	}
	return errors.Join(errs...)
}

// NodeSqliteStorageOptions configures a file-backed durable storage connection. A nil field selects its default.
type NodeSqliteStorageOptions struct {
	// WalAutoCheckpointPages is the SQLite WAL auto-checkpoint threshold. SQLite and this adapter default to 1,000
	// pages; 0 disables it.
	WalAutoCheckpointPages *int
	// BusyTimeoutMs is how long SQLite waits for a competing file lock. SQLite defaults to 0; this adapter defaults to
	// 5,000 ms.
	BusyTimeoutMs *int
}

const (
	defaultWalAutoCheckpointPages = 1_000
	defaultBusyTimeoutMs          = 5_000
)

// OpenNodeSqliteDatabase opens and configures a native SQLite database facade, creating the parent directory.
func OpenNodeSqliteDatabase(path string, options NodeSqliteStorageOptions) (*NodeSqliteDatabase, error) {
	checkpointPages := defaultWalAutoCheckpointPages
	if options.WalAutoCheckpointPages != nil {
		checkpointPages = *options.WalAutoCheckpointPages
	}
	timeout := defaultBusyTimeoutMs
	if options.BusyTimeoutMs != nil {
		timeout = *options.BusyTimeoutMs
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return nil, err
		}
	}
	database, err := NewDatabaseSync(path, DatabaseSyncOptions{TimeoutMs: timeout})
	if err != nil {
		return nil, err
	}
	adapter := NewNodeSqliteDatabase(database)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		fmt.Sprintf("PRAGMA wal_autocheckpoint = %d", checkpointPages),
	} {
		if err := adapter.Exec(pragma); err != nil {
			// Preserve the configuration failure.
			_ = adapter.Close()
			return nil, err
		}
	}
	return adapter, nil
}

// OpenNodeSqliteStorage opens or creates file-backed durable storage.
func OpenNodeSqliteStorage(path string, options NodeSqliteStorageOptions) (*durablesqlite.SqliteStorage, error) {
	database, err := OpenNodeSqliteDatabase(path, options)
	if err != nil {
		return nil, err
	}
	return durablesqlite.Open(database)
}
