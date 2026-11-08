// Package node adapts a native SQLite connection to the portable Chord SQLite facade. It is the Go counterpart of
// Pi's node:sqlite driver and the only Chord SQLite package that links a SQLite driver (modernc.org/sqlite, pure Go,
// CGO-free).
package node

// Ports packages/chord/src/sqlite/node.ts

import (
	"container/list"
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

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
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
	// prepared is called with the SQL text of every statement Prepare compiles (ObservePrepare).
	prepared func(sqlText string)
}

// ObservePrepare calls observe with the SQL text of every statement Prepare compiles from now on. It stands in for a
// subclass overriding DatabaseSync.prepare, as Pi's storage tests count compilations.
// pig additive (D104): Go has no subclassing of the connection, so an observer takes the override's place.
func (database *DatabaseSync) ObservePrepare(observe func(sqlText string)) {
	database.mu.Lock()
	defer database.mu.Unlock()
	database.prepared = observe
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
	database.mu.Lock()
	observe := database.prepared
	database.mu.Unlock()
	if observe != nil {
		observe(sqlText)
	}
	statement, err := conn.PrepareContext(context.Background(), sqlText)
	if err != nil {
		return nil, err
	}
	return &StatementSync{database: database, sqlText: sqlText, statement: statement, tracksSmallIntegers: readsRows(sqlText)}, nil
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
//
// Since SQLite 3.54 a statement whose plan can depend on a bound integer (a "col = ?" term) is planned again when that
// parameter changes between the integers 0 and 1 and any other value, though the plan is the same. Pi's fork-depth head
// lookup binds the root conversation (id 1) at the end of each walk and a deeper one at the start of the next, so one
// statement would re-plan on every step. A statement therefore keeps one compiled copy per pattern of 0/1 parameters
// it is run with (smallIntegerMask): the first pattern uses statement, others compile lazily from sqlText. Copies are
// never shared across patterns, so no execution changes a parameter across the 0/1 boundary.
//
// A StatementSync is used by one goroutine at a time: its executor runs every operation through the database's serial
// queue (or an active transaction handle), and the statement cache lock covers only the lookup. statementMask,
// statementUsed and variants take no lock of their own. A parameter at index 31 or later is not tracked (the 0/1
// patterns are at most 31 bits), so a statement that flips such a parameter across 0/1 is planned again as before. Only
// SELECT and WITH statements are tracked, with at most maxStatementVariants extra copies; the copies are closed with the
// statement when the cache evicts it or the database closes.
type StatementSync struct {
	database  *DatabaseSync
	sqlText   string
	statement *sql.Stmt
	// statementMask is the smallIntegerMask statement is bound with; statementUsed reports whether it was decided.
	statementMask uint32
	statementUsed bool
	variants      map[uint32]*sql.Stmt
	// tracksSmallIntegers is set for a statement that reads (SELECT or WITH), the kind the copies were
	// measured on; an INSERT of integer columns would only pay for them.
	tracksSmallIntegers bool
}

// maxStatementVariants bounds the extra compiled copies of one statement; past it the statement runs as before.
const maxStatementVariants = 4

// readsRows reports whether sqlText starts with SELECT or WITH, after white space.
func readsRows(sqlText string) bool {
	text := strings.TrimLeft(sqlText, " \t\r\n")
	for _, keyword := range []string{"SELECT", "WITH"} {
		if len(text) > len(keyword) && strings.EqualFold(text[:len(keyword)], keyword) {
			if next := text[len(keyword)]; next == ' ' || next == '\t' || next == '\r' || next == '\n' || next == '(' {
				return true
			}
		}
	}
	return false
}

// smallIntegerMask has bit i set when params[i] (i < 31) is a bool or an integer equal to 0 or 1.
func smallIntegerMask(params []any) (mask uint32) {
	for index, param := range params {
		if index >= 31 {
			break
		}
		switch value := param.(type) {
		case int64:
			if uint64(value) <= 1 {
				mask |= 1 << index
			}
		case int:
			if uint(value) <= 1 {
				mask |= 1 << index
			}
		case bool:
			mask |= 1 << index
		}
	}
	return mask
}

// open returns the compiled copy of the statement to run with params.
func (statement *StatementSync) open(params []any) (*sql.Stmt, error) {
	conn, err := statement.database.connection()
	if err != nil {
		return nil, err
	}
	if !statement.tracksSmallIntegers {
		return statement.statement, nil
	}
	mask := smallIntegerMask(params)
	if !statement.statementUsed {
		statement.statementMask, statement.statementUsed = mask, true
	}
	if mask == statement.statementMask {
		return statement.statement, nil
	}
	if variant := statement.variants[mask]; variant != nil {
		return variant, nil
	}
	if len(statement.variants) >= maxStatementVariants {
		return statement.statement, nil
	}
	variant, err := conn.PrepareContext(context.Background(), statement.sqlText) //portlint:allow ctxbackground the same synchronous prepare as DatabaseSync.Prepare: node:sqlite takes no signal
	if err != nil {
		return nil, err
	}
	if statement.variants == nil {
		statement.variants = map[uint32]*sql.Stmt{}
	}
	statement.variants[mask] = variant
	return variant, nil
}

// Run executes the statement with positional bindings.
func (statement *StatementSync) Run(params ...any) error {
	prepared, err := statement.open(params)
	if err != nil {
		return err
	}
	_, err = prepared.ExecContext(context.Background(), params...)
	return err
}

// RunCounted executes the statement and returns the rows it changed.
func (statement *StatementSync) RunCounted(params ...any) (int64, error) {
	prepared, err := statement.open(params)
	if err != nil {
		return 0, err
	}
	result, err := prepared.ExecContext(context.Background(), params...) //portlint:allow ctxbackground node:sqlite StatementSync.run (node.ts:102) is synchronous and takes no signal; no caller context exists, as in Run
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// EachText passes the first column of every result row to fn as it is read; the bytes are valid until fn returns.
func (statement *StatementSync) EachText(params []any, fn func(text []byte) error) (err error) {
	prepared, err := statement.open(params)
	if err != nil {
		return err
	}
	rows, err := prepared.QueryContext(context.Background(), params...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var text sql.RawBytes
		if err := rows.Scan(&text); err != nil {
			return err
		}
		if err := fn(text); err != nil {
			return err
		}
	}
	return rows.Err()
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
	prepared, err := statement.open(params)
	if err != nil {
		return nil, err
	}
	rows, err := prepared.QueryContext(context.Background(), params...)
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

func (statement *StatementSync) close() error {
	err := statement.statement.Close()
	for _, variant := range statement.variants {
		err = errors.Join(err, variant.Close())
	}
	return err
}

// connection is the native connection surface NodeSqliteDatabase drives; *DatabaseSync is its one production implementation.
type connection interface {
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
// database and its transaction handles share them across transactions. The cache keeps the statementCacheLimit most
// recently used statements, so callers generating SQL of unbounded shapes cannot grow it without bound.
type nodeSqliteExecutor struct {
	database   connection
	statements *statementCache
	// runOperation brackets one operation: the database queues it, a transaction handle checks that it is active.
	runOperation func(operation func() error) error
}

// statementCacheLimit is how many prepared statements one connection keeps.
const statementCacheLimit = 256

// statementCache holds prepared statements by SQL text, least recently used first.
type statementCache struct {
	mu         sync.Mutex
	statements map[string]*list.Element
	order      list.List
}

type cachedStatement struct {
	sqlText   string
	statement *StatementSync
}

func newStatementCache() *statementCache {
	return &statementCache{statements: map[string]*list.Element{}}
}

func (executor *nodeSqliteExecutor) statement(sqlText string) (*StatementSync, error) {
	cache := executor.statements
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if element := cache.statements[sqlText]; element != nil {
		// Recency matters only once evictions come near: a small cache keeps one lookup per statement.
		if len(cache.statements) > statementCacheLimit/2 {
			cache.order.MoveToBack(element)
		}
		return element.Value.(*cachedStatement).statement, nil
	}
	statement, err := executor.database.Prepare(sqlText)
	if err != nil {
		return nil, err
	}
	cache.statements[sqlText] = cache.order.PushBack(&cachedStatement{sqlText: sqlText, statement: statement})
	if len(cache.statements) > statementCacheLimit {
		oldest := cache.order.Front()
		cache.order.Remove(oldest)
		evicted := oldest.Value.(*cachedStatement)
		delete(cache.statements, evicted.sqlText)
		// Operations run serially, so the evicted statement is not executing; the one being returned is the newest.
		_ = evicted.statement.close()
	}
	return statement, nil
}

func (executor *nodeSqliteExecutor) Exec(sqlText string) error {
	return executor.runOperation(func() error { return executor.database.Exec(sqlText) })
}

func (executor *nodeSqliteExecutor) Run(sqlText string, params ...chordsqlite.SqliteValue) error {
	return executor.runOperation(func() error {
		statement, err := executor.statement(sqlText)
		if err != nil {
			return err
		}
		return statement.Run(params...)
	})
}

// RunCounted executes one statement and returns the rows it changed.
func (executor *nodeSqliteExecutor) RunCounted(sqlText string, params ...chordsqlite.SqliteValue) (changed int64, err error) {
	err = executor.runOperation(func() error {
		statement, prepareErr := executor.statement(sqlText)
		if prepareErr != nil {
			return prepareErr
		}
		changed, prepareErr = statement.RunCounted(params...)
		return prepareErr
	})
	return changed, err
}

// EachText runs a query whose first column is text and passes each value to fn without building result rows.
func (executor *nodeSqliteExecutor) EachText(sqlText string, params []chordsqlite.SqliteValue, fn func(text []byte) error) error {
	return executor.runOperation(func() error {
		statement, err := executor.statement(sqlText)
		if err != nil {
			return err
		}
		return statement.EachText(params, fn)
	})
}

func (executor *nodeSqliteExecutor) Get(
	sqlText string, params ...chordsqlite.SqliteValue,
) (row chordsqlite.SqliteRow, err error) {
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
	sqlText string, params ...chordsqlite.SqliteValue,
) (rows []chordsqlite.SqliteRow, err error) {
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
	// readOnly is a connection opened with NodeSqliteDatabaseOptions.ReadOnly: its transactions are deferred (they read
	// one snapshot; it cannot take the write lock) and its close checkpoints nothing.
	readOnly bool
}

// NewNodeSqliteDatabase adapts database; the adapter owns it from now on. It is Pi's `new NodeSqliteDatabase(database: DatabaseSync)`.
func NewNodeSqliteDatabase(database *DatabaseSync) *NodeSqliteDatabase {
	return newNodeSqliteDatabase(database)
}

// newNodeSqliteDatabase adapts any native connection, so a test can wrap one to observe or fail its calls.
func newNodeSqliteDatabase(database connection) *NodeSqliteDatabase {
	adapter := &NodeSqliteDatabase{access: newSerialOperationQueue()}
	adapter.nodeSqliteExecutor = nodeSqliteExecutor{
		database:     database,
		statements:   newStatementCache(),
		runOperation: adapter.run,
	}
	return adapter
}

func (adapter *NodeSqliteDatabase) run(operation func() error) error {
	release := adapter.access.acquire()
	defer release()
	return operation()
}

// Transaction runs callback inside BEGIN IMMEDIATE ... COMMIT (BEGIN for a read-only connection) and rolls back when it
// fails or panics.
func (adapter *NodeSqliteDatabase) Transaction(
	callback func(transaction chordsqlite.SqliteExecutor) error,
) (err error) {
	release := adapter.access.acquire()
	defer release()
	begin := "BEGIN IMMEDIATE"
	if adapter.readOnly {
		begin = "BEGIN"
	}
	if err := adapter.database.Exec(begin); err != nil {
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
	for sqlText, element := range adapter.statements.statements {
		errs = append(errs, element.Value.(*cachedStatement).statement.close())
		delete(adapter.statements.statements, sqlText)
	}
	adapter.statements.order.Init()
	adapter.statements.mu.Unlock()
	var checkpointErr error
	if !adapter.readOnly {
		checkpointErr = adapter.database.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	// Pi closes in a finally block, so a close failure replaces the checkpoint failure.
	if closeErr := adapter.database.Close(); closeErr != nil {
		return closeErr
	}
	if checkpointErr != nil {
		return checkpointErr
	}
	return errors.Join(errs...)
}

// NodeSqliteDatabaseOptions configures a file-backed SQLite connection. A nil field selects its default.
type NodeSqliteDatabaseOptions struct {
	// WalAutoCheckpointPages is the SQLite WAL auto-checkpoint threshold. SQLite and this adapter default to 1,000
	// pages; 0 disables it.
	WalAutoCheckpointPages *int
	// BusyTimeoutMs is how long SQLite waits for a competing file lock. SQLite defaults to 0; this adapter defaults to
	// 5,000 ms.
	BusyTimeoutMs *int
	// ReadOnly opens an existing file for reads (another process may write it): no directory is made, nothing is
	// configured or checkpointed, and transactions are deferred (`BEGIN`), reading one snapshot. Pi's `mode: "read-only"`.
	ReadOnly bool
}

const (
	defaultWalAutoCheckpointPages = 1_000
	defaultBusyTimeoutMs          = 5_000
)

// OpenNodeSqliteDatabase opens and configures a native SQLite database facade, creating the parent directory.
func OpenNodeSqliteDatabase(path string, options NodeSqliteDatabaseOptions) (*NodeSqliteDatabase, error) {
	checkpointPages := defaultWalAutoCheckpointPages
	if options.WalAutoCheckpointPages != nil {
		checkpointPages = *options.WalAutoCheckpointPages
	}
	timeout := defaultBusyTimeoutMs
	if options.BusyTimeoutMs != nil {
		timeout = *options.BusyTimeoutMs
	}
	if options.ReadOnly {
		database, err := NewDatabaseSync(path, DatabaseSyncOptions{TimeoutMs: timeout, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		adapter := NewNodeSqliteDatabase(database)
		adapter.readOnly = true
		return adapter, nil
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
