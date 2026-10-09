// Package cloudflare adapts the SQLite storage of a Cloudflare Durable Object to the portable durable SQLite facade. It
// is the Go counterpart of Pi's `@earendil-works/pi-durable/storage/sqlite/cloudflare` and links no SQLite driver or
// Workers binding: the caller supplies the Durable Object's `ctx.storage` as a DurableObjectSqliteStorage.
package cloudflare

// Ports packages/durable/src/storage/sqlite/cloudflare.ts

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	durablesqlite "github.com/MichaelKinsy/PiG/durable/storage/sqlite"
)

// maxSafeInteger is JavaScript's Number.MAX_SAFE_INTEGER, the largest integer a Durable Object SQL binding keeps.
const maxSafeInteger = 1<<53 - 1

// DurableObjectSqlValue is a value a Durable Object SQL binding accepts and returns: []byte (an ArrayBuffer), string, a
// number (float64 or any Go integer), or nil.
type DurableObjectSqlValue = any

// DurableObjectSqlCursor is the result of one statement.
type DurableObjectSqlCursor interface {
	// ToArray returns every result row keyed by column name; empty for a statement that returns none.
	ToArray() ([]map[string]DurableObjectSqlValue, error)
}

// DurableObjectSql is the `ctx.storage.sql` of a SQLite-backed Durable Object.
type DurableObjectSql interface {
	// Exec runs one statement with positional bindings; without bindings it may run several statements.
	Exec(query string, bindings ...DurableObjectSqlValue) (DurableObjectSqlCursor, error)
}

// DurableObjectSqliteStorage is the parts of a SQLite-backed Durable Object's `ctx.storage` this adapter uses.
type DurableObjectSqliteStorage interface {
	Sql() DurableObjectSql
	// Transaction runs closure, committing when it returns nil and rolling back when it returns an error, which
	// Transaction returns.
	Transaction(closure func() error) error
}

// RangeError reports a binding that Durable Object SQL cannot keep exactly: it binds numbers as doubles, so an integer
// outside the safe integer range would lose precision.
type RangeError struct{ Value int64 }

func (err *RangeError) Error() string {
	return fmt.Sprintf("Durable Object SQL cannot bind %d without losing precision", err.Value)
}

var errTransactionInactive = errors.New("SQLite transaction handle is no longer active")

// serialQueue runs operations one at a time in call order, so a transaction excludes every other operation.
type serialQueue struct {
	mu   sync.Mutex
	tail chan struct{}
}

func newSerialQueue() *serialQueue {
	ready := make(chan struct{})
	close(ready)
	return &serialQueue{tail: ready}
}

// run takes the next turn, blocks until every earlier turn has finished, and runs operation.
func (queue *serialQueue) run(operation func() error) error {
	queue.mu.Lock()
	previous := queue.tail
	turn := make(chan struct{})
	queue.tail = turn
	queue.mu.Unlock()
	<-previous
	defer close(turn)
	return operation()
}

// bindValue converts a binding for Durable Object SQL: a []byte is copied, and an integer outside the safe integer
// range is a RangeError.
func bindValue(value durablesqlite.SqliteValue) (DurableObjectSqlValue, error) {
	switch typed := value.(type) {
	case []byte:
		return slices.Clone(typed), nil
	case int64:
		return typed, checkSafe(typed)
	case int:
		return typed, checkSafe(int64(typed))
	}
	return value, nil
}

func checkSafe(value int64) error {
	if value < -maxSafeInteger || value > maxSafeInteger {
		return &RangeError{Value: value}
	}
	return nil
}

func bind(params []durablesqlite.SqliteValue) ([]DurableObjectSqlValue, error) {
	bindings := make([]DurableObjectSqlValue, len(params))
	for index, param := range params {
		bound, err := bindValue(param)
		if err != nil {
			return nil, err
		}
		bindings[index] = bound
	}
	return bindings, nil
}

// durableObjectExecutor executes SQL through `ctx.storage.sql`, which has no separate prepare step.
type durableObjectExecutor struct {
	storage DurableObjectSqliteStorage
	// check returns an error when this handle may no longer run SQL.
	check func() error
}

func (executor *durableObjectExecutor) Exec(sql string) error {
	if err := executor.check(); err != nil {
		return err
	}
	_, err := executor.storage.Sql().Exec(sql)
	return err
}

func (executor *durableObjectExecutor) Run(sql string, params ...durablesqlite.SqliteValue) error {
	if err := executor.check(); err != nil {
		return err
	}
	bindings, err := bind(params)
	if err != nil {
		return err
	}
	_, err = executor.storage.Sql().Exec(sql, bindings...)
	return err
}

func (executor *durableObjectExecutor) Get(sql string, params ...durablesqlite.SqliteValue) (durablesqlite.SqliteRow, error) {
	rows, err := executor.All(sql, params...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (executor *durableObjectExecutor) All(sql string, params ...durablesqlite.SqliteValue) ([]durablesqlite.SqliteRow, error) {
	if err := executor.check(); err != nil {
		return nil, err
	}
	bindings, err := bind(params)
	if err != nil {
		return nil, err
	}
	cursor, err := executor.storage.Sql().Exec(sql, bindings...)
	if err != nil {
		return nil, err
	}
	rows, err := cursor.ToArray()
	if err != nil {
		return nil, err
	}
	result := make([]durablesqlite.SqliteRow, len(rows))
	copy(result, rows)
	return result, nil
}

// DurableObjectSqliteDatabase is the SqliteDatabase adapter for the SQLite storage of a Cloudflare Durable Object.
// Integers are numbers; binding an integer outside the safe integer range is a RangeError.
type DurableObjectSqliteDatabase struct {
	durableObjectExecutor
	queue *serialQueue
}

var _ durablesqlite.SqliteDatabase = (*DurableObjectSqliteDatabase)(nil)

// NewDurableObjectSqliteDatabase adapts the `ctx.storage` of a SQLite-backed Durable Object.
func NewDurableObjectSqliteDatabase(storage DurableObjectSqliteStorage) *DurableObjectSqliteDatabase {
	return &DurableObjectSqliteDatabase{
		durableObjectExecutor: durableObjectExecutor{storage: storage, check: func() error { return nil }},
		queue:                 newSerialQueue(),
	}
}

// Exec runs SQL text without bindings behind every earlier operation.
func (database *DurableObjectSqliteDatabase) Exec(sql string) error {
	return database.queue.run(func() error { return database.durableObjectExecutor.Exec(sql) })
}

// Run executes one statement with positional bindings behind every earlier operation.
func (database *DurableObjectSqliteDatabase) Run(sql string, params ...durablesqlite.SqliteValue) error {
	return database.queue.run(func() error { return database.durableObjectExecutor.Run(sql, params...) })
}

// Get returns the first result row, or nil when there is none.
func (database *DurableObjectSqliteDatabase) Get(sql string, params ...durablesqlite.SqliteValue) (row durablesqlite.SqliteRow, err error) {
	err = database.queue.run(func() (err error) {
		row, err = database.durableObjectExecutor.Get(sql, params...)
		return err
	})
	return row, err
}

// All returns every result row.
func (database *DurableObjectSqliteDatabase) All(sql string, params ...durablesqlite.SqliteValue) (rows []durablesqlite.SqliteRow, err error) {
	err = database.queue.run(func() (err error) {
		rows, err = database.durableObjectExecutor.All(sql, params...)
		return err
	})
	return rows, err
}

// Transaction runs callback in `ctx.storage.Transaction`, which commits when the callback returns nil and rolls back
// when it fails. The handle the callback receives is invalid once the callback returns.
func (database *DurableObjectSqliteDatabase) Transaction(callback func(transaction durablesqlite.SqliteExecutor) error) error {
	return database.queue.run(func() error {
		return database.storage.Transaction(func() error {
			var mu sync.Mutex
			active := true
			handle := &durableObjectExecutor{storage: database.storage, check: func() error {
				mu.Lock()
				defer mu.Unlock()
				if !active {
					return errTransactionInactive
				}
				return nil
			}}
			defer func() {
				mu.Lock()
				active = false
				mu.Unlock()
			}()
			return callback(handle)
		})
	})
}

// Close waits for queued work. The Durable Object owns its storage, so nothing else closes.
func (database *DurableObjectSqliteDatabase) Close() error {
	return database.queue.run(func() error { return nil })
}

// OpenDurableObjectSqliteStorage opens durable storage on a SQLite-backed Durable Object's `ctx.storage`.
func OpenDurableObjectSqliteStorage(storage DurableObjectSqliteStorage) (*durablesqlite.SqliteStorage, error) {
	return durablesqlite.Open(NewDurableObjectSqliteDatabase(storage))
}
