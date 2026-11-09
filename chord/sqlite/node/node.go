// Package node adapts a native SQLite connection to the portable Chord SQLite facade. It is the Go counterpart of
// Pi's node:sqlite driver and the only Chord SQLite package that links a SQLite driver (modernc.org/sqlite's driver
// layer, vendored in third_party/sqlite, over its pure Go, CGO-free SQLite library).
package node

// Ports packages/chord/src/sqlite/node.ts

import (
	"container/list"
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	// The pure-Go SQLite driver. The connection is opened through its driver.Connector and driven directly: one
	// dedicated connection needs none of database/sql's pooling, argument conversion or row scanning.
	"github.com/MichaelKinsy/PiG/third_party/sqlite"
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
//
// The connection is opened in SQLite's multi-thread mode (see dataSourceName): a DatabaseSync, and every StatementSync
// and row set taken from it, must be used by one goroutine at a time, and a goroutine hand-off needs a happens-before
// edge (a channel, a mutex). Two goroutines inside the connection at once corrupt the driver's memory instead of waiting
// for each other. NodeSqliteDatabase is the safe way to share a connection: its serial queue holds every operation, a
// transaction as a whole, and Close.
type DatabaseSync struct {
	mu   sync.Mutex
	conn driver.Conn
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

// newConnector opens the driver's connector for a data source name; tests replace it to see the name NewDatabaseSync
// builds.
var newConnector = sqlite.NewConnector

// NewDatabaseSync opens path (or ":memory:") as one dedicated connection.
func NewDatabaseSync(path string, options DatabaseSyncOptions) (*DatabaseSync, error) {
	connector, err := newConnector(dataSourceName(path, options.ReadOnly))
	if err != nil {
		return nil, err
	}
	conn, err := connector.Connect(context.Background())
	if err != nil {
		return nil, err
	}
	database := &DatabaseSync{conn: conn}
	if err := database.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", max(options.TimeoutMs, 0))); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

// threadingMode is the driver's DSN parameter that opens a connection in SQLite's multi-thread mode
// (SQLITE_OPEN_NOMUTEX).
const threadingMode = "_threading_mode=multi-thread"

// dataSourceName keeps a plain path and uses a file: URI only when the path would otherwise be read as driver query
// parameters or when read-only mode is requested. Every connection opens in multi-thread mode: a DatabaseSync is used
// by one goroutine at a time (NodeSqliteDatabase's serial queue holds every operation, Close included; the driver's
// context interrupt only sets an atomic flag and calls sqlite3_interrupt), so SQLite's per-connection mutex would only
// cost time on every call into SQLite.
// modernc DSN nomutex PR (owner to post); draft: ~/PiG-launch-newer/handoffs/next/modernc-dsn-nomutex-pr.md
func dataSourceName(path string, readOnly bool) string {
	if !readOnly && !strings.ContainsAny(path, "?#") {
		return path + "?" + threadingMode
	}
	location := url.URL{Scheme: "file", Opaque: (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath()}
	location.RawQuery = threadingMode
	if readOnly {
		location.RawQuery = "mode=ro&" + threadingMode
	}
	return location.String()
}

func (database *DatabaseSync) connection() (driver.Conn, error) {
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
	_, err = conn.(driver.ExecerContext).ExecContext(context.Background(), sqlText, nil)
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
	statement, err := conn.(driver.ConnPrepareContext).PrepareContext(context.Background(), sqlText)
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
	return conn.Close()
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
	statement driver.Stmt
	// statementMask is the smallIntegerMask statement is bound with; statementUsed reports whether it was decided.
	statementMask uint32
	statementUsed bool
	variants      map[uint32]driver.Stmt
	// tracksSmallIntegers is set for a statement that reads (SELECT or WITH), the kind the copies were
	// measured on; an INSERT of integer columns would only pay for them.
	tracksSmallIntegers bool
	// bindings is the argument list of the call in progress, reused across calls (operations on a connection are
	// serial and the driver copies every argument at bind time).
	bindings []driver.NamedValue
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
func (statement *StatementSync) open(params []any) (driver.Stmt, error) {
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
	variant, err := conn.(driver.ConnPrepareContext).PrepareContext(context.Background(), statement.sqlText) //portlint:allow ctxbackground the same synchronous prepare as DatabaseSync.Prepare: node:sqlite takes no signal
	if err != nil {
		return nil, err
	}
	if statement.variants == nil {
		statement.variants = map[uint32]driver.Stmt{}
	}
	statement.variants[mask] = variant
	return variant, nil
}

// bind converts params to the driver's arguments, as database/sql's default argument converter does: integers widen
// to int64, and a value of another type goes through driver.DefaultParameterConverter.
func (statement *StatementSync) bind(params []any) ([]driver.NamedValue, error) {
	arguments := statement.bindings[:0]
	for index, param := range params {
		var value driver.Value
		switch typed := param.(type) {
		case nil, int64, float64, bool, string, []byte:
			value = typed
		case int:
			value = int64(typed)
		default:
			converted, err := driver.DefaultParameterConverter.ConvertValue(param)
			if err != nil {
				return nil, fmt.Errorf("sql: converting argument $%d type: %w", index+1, err)
			}
			value = converted
		}
		arguments = append(arguments, driver.NamedValue{Ordinal: index + 1, Value: value})
	}
	statement.bindings = arguments
	return arguments, nil
}

// Run executes the statement with positional bindings.
func (statement *StatementSync) Run(params ...any) error {
	_, err := statement.exec(params)
	return err
}

func (statement *StatementSync) exec(params []any) (driver.Result, error) {
	prepared, err := statement.open(params)
	if err != nil {
		return nil, err
	}
	arguments, err := statement.bind(params)
	if err != nil {
		return nil, err
	}
	return prepared.(driver.StmtExecContext).ExecContext(context.Background(), arguments) //portlint:allow ctxbackground node:sqlite StatementSync.run (node.ts:102) is synchronous and takes no signal; no caller context exists
}

// RunCounted executes the statement and returns the rows it changed.
func (statement *StatementSync) RunCounted(params ...any) (int64, error) {
	result, err := statement.exec(params)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// each runs the statement and calls fn with the column names and the values of every result row, as the row is read,
// until fn returns false or the rows end. The values slice is reused for the next row.
func (statement *StatementSync) each(params []any, fn func(columns []string, values []driver.Value) (bool, error)) (err error) {
	prepared, err := statement.open(params)
	if err != nil {
		return err
	}
	arguments, err := statement.bind(params)
	if err != nil {
		return err
	}
	rows, err := prepared.(driver.StmtQueryContext).QueryContext(context.Background(), arguments) //portlint:allow ctxbackground node:sqlite StatementSync.all (node.ts) is synchronous and takes no signal
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	columns := rows.Columns()
	values := make([]driver.Value, len(columns))
	for {
		if err := rows.Next(values); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		more, err := fn(columns, values)
		if err != nil || !more {
			return err
		}
	}
}

// EachText passes the first column of every result row to fn as it is read; the bytes are valid until fn returns and
// must not be modified.
func (statement *StatementSync) EachText(params []any, fn func(text []byte) error) error {
	return statement.each(params, func(_ []string, values []driver.Value) (bool, error) {
		var text []byte
		switch value := values[0].(type) {
		case string:
			// The driver made the string for this row alone, so its bytes are read in place instead of copied again.
			text = unsafe.Slice(unsafe.StringData(value), len(value)) //nolint:gosec // G103: a read-only view of a string the driver made for this row alone, valid until fn returns
		case []byte:
			text = value
		default:
			return false, fmt.Errorf("sql: Scan error on column index 0: unsupported Scan, storing driver.Value type %T into type *sql.RawBytes", value)
		}
		return true, fn(text)
	})
}

// EachRow passes the values of every result row to fn as it is read, in column order, as All would put them in the
// row's map; the slice is reused for the next row and the values it holds are the caller's to keep.
// pig additive (D104): row reading without per-row maps has no Pi counterpart.
func (statement *StatementSync) EachRow(params []any, fn func(values []any) error) error {
	return statement.each(params, func(_ []string, values []driver.Value) (bool, error) {
		// driver.Value is an interface{} too: the same memory, read as []any.
		return true, fn(unsafe.Slice((*any)(unsafe.SliceData(values)), len(values))) //nolint:gosec // G103: driver.Value and any are the same interface type, so the slice's memory is read as it is
	})
}

// Get returns the first result row, or nil when there is none.
func (statement *StatementSync) Get(params ...any) (row map[string]any, err error) {
	err = statement.each(params, func(columns []string, values []driver.Value) (bool, error) {
		row = rowMap(columns, values)
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return row, nil
}

// All returns every result row.
func (statement *StatementSync) All(params ...any) ([]map[string]any, error) {
	result := []map[string]any{}
	err := statement.each(params, func(columns []string, values []driver.Value) (bool, error) {
		result = append(result, rowMap(columns, values))
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func rowMap(columns []string, values []driver.Value) map[string]any {
	row := make(map[string]any, len(columns))
	for index, column := range columns {
		row[column] = values[index]
	}
	return row
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
	running bool
	// waiting holds the turns behind the running one, oldest first; release hands the queue to the first.
	waiting []chan struct{}
}

func newSerialOperationQueue() *serialOperationQueue { return &serialOperationQueue{} }

// acquire takes the next turn and blocks until every earlier turn has been released.
func (queue *serialOperationQueue) acquire() {
	queue.mu.Lock()
	if !queue.running {
		queue.running = true
		queue.mu.Unlock()
		return
	}
	turn := make(chan struct{})
	queue.waiting = append(queue.waiting, turn)
	queue.mu.Unlock()
	<-turn
}

// release ends the running turn and starts the oldest waiting one.
func (queue *serialOperationQueue) release() {
	queue.mu.Lock()
	if len(queue.waiting) == 0 {
		queue.running = false
		queue.mu.Unlock()
		return
	}
	turn := queue.waiting[0]
	queue.waiting[0] = nil
	queue.waiting = queue.waiting[1:]
	queue.mu.Unlock()
	close(turn)
}

// queued reports how many turns wait behind the running one.
func (queue *serialOperationQueue) queued() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return len(queue.waiting)
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

// EachRow runs a query and passes the values of each result row to fn, in column order, without building result rows.
// pig additive (D104): row reading without per-row maps has no Pi counterpart.
func (executor *nodeSqliteExecutor) EachRow(sqlText string, params []chordsqlite.SqliteValue, fn func(values []chordsqlite.SqliteValue) error) error {
	return executor.runOperation(func() error {
		statement, err := executor.statement(sqlText)
		if err != nil {
			return err
		}
		return statement.EachRow(params, fn)
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
	adapter.access.acquire()
	defer adapter.access.release()
	return operation()
}

// Transaction runs callback inside BEGIN IMMEDIATE ... COMMIT (BEGIN for a read-only connection) and rolls back when it
// fails or panics. The transaction handle runs its operations directly on the connection: it is for the goroutine
// that runs callback (or goroutines it hands the handle to one at a time), and not past the callback's return, because
// the connection is in SQLite's multi-thread mode and another goroutine's operation, queued behind this transaction,
// starts the moment it settles.
func (adapter *NodeSqliteDatabase) Transaction(
	callback func(transaction chordsqlite.SqliteExecutor) error,
) (err error) {
	adapter.access.acquire()
	defer adapter.access.release()
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
	adapter.access.acquire()
	defer adapter.access.release()
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
