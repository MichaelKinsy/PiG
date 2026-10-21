package telemetrysqlite

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordsqlite "github.com/MichaelKinsy/PiG/chord/sqlite"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// spyDatabase is `vi.spyOn(db, "transaction")`: it counts Transaction calls and, when hook is set, runs it instead of
// calling the wrapped database directly. hook receives the original Transaction as `original`, as the upstream
// mockImplementation closes over `db.transaction.bind(db)`.
type spyDatabase struct {
	chordsqlite.SqliteDatabase
	calls atomic.Int32

	mu   sync.Mutex
	hook func(callback func(chordsqlite.SqliteExecutor) error, original func(func(chordsqlite.SqliteExecutor) error) error) error
}

func (database *spyDatabase) setHook(hook func(callback func(chordsqlite.SqliteExecutor) error, original func(func(chordsqlite.SqliteExecutor) error) error) error) {
	database.mu.Lock()
	database.hook = hook
	database.mu.Unlock()
}

func (database *spyDatabase) Transaction(callback func(chordsqlite.SqliteExecutor) error) error {
	database.calls.Add(1)
	database.mu.Lock()
	hook := database.hook
	database.mu.Unlock()
	if hook == nil {
		return database.SqliteDatabase.Transaction(callback)
	}
	return hook(callback, database.SqliteDatabase.Transaction)
}

func openMemory(t *testing.T) *spyDatabase {
	t.Helper()
	inner, err := node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &spyDatabase{SqliteDatabase: inner}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// mockDate is `vi.spyOn(Date, "now").mockReturnValue(initial)`, restored when the test ends.
func mockDate(t *testing.T, initial float64) *jsclock.Mock {
	t.Helper()
	mock, restore := jsclock.MockDateNow(initial)
	t.Cleanup(restore)
	return mock
}

// fixture is upstream's fixture(): an in-memory database and a recorder over it.
type fixture struct {
	t         *testing.T
	db        *spyDatabase
	telemetry *SqliteTelemetry
}

func newFixture(t *testing.T, options SqliteTelemetryOptions) *fixture {
	t.Helper()
	db := openMemory(t)
	recorder, err := CreateSqliteTelemetry(db, options)
	check(t, err)
	db.calls.Store(0)
	f := &fixture{t: t, db: db, telemetry: recorder}
	// `await using`: close the recorder, then the database, even when the test already closed them.
	t.Cleanup(func() {
		closeErr := recorder.Close()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		if closeErr != nil {
			t.Logf("cleanup close: %v", closeErr)
		}
	})
	return f
}

func (f *fixture) all(sql string, params ...chordsqlite.SqliteValue) []chordsqlite.SqliteRow {
	f.t.Helper()
	rows, err := f.db.All(sql, params...)
	check(f.t, err)
	return rows
}

func (f *fixture) names(sql string) []string {
	f.t.Helper()
	var names []string
	for _, row := range f.all(sql) {
		names = append(names, row["name"].(string))
	}
	return names
}

func (f *fixture) flush() {
	f.t.Helper()
	check(f.t, f.telemetry.Flush())
}

func (f *fixture) span(name string) {
	f.t.Helper()
	check(f.t, f.telemetry.StartSpan(telemetry.SpanOptions{Name: name}, func(telemetry.TelemetrySpan) error { return nil }))
}

// future is a call started without waiting for it, the counterpart of an unawaited Promise.
type future struct {
	done chan struct{}
	err  error
}

func start(operation func() error) *future {
	result := &future{done: make(chan struct{})}
	go func() {
		defer close(result.done)
		result.err = operation()
	}()
	return result
}

func (result *future) wait(t *testing.T) error {
	t.Helper()
	select {
	case <-result.done:
		return result.err
	case <-time.After(10 * time.Second):
		t.Fatal("operation did not settle")
		return nil
	}
}

// blocker is the `entered`/`release` pair of Promise.withResolvers in upstream's transaction mocks.
type blocker struct {
	enteredOnce sync.Once
	releaseOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

// newBlocker also releases the transaction when the test ends, so a failed assertion cannot leave a flush blocked.
func newBlocker(t *testing.T) *blocker {
	t.Helper()
	b := &blocker{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(b.open)
	return b
}

func (b *blocker) open() { b.releaseOnce.Do(func() { close(b.release) }) }

func (b *blocker) hook(callback func(chordsqlite.SqliteExecutor) error, original func(func(chordsqlite.SqliteExecutor) error) error) error {
	b.enteredOnce.Do(func() { close(b.entered) })
	<-b.release
	return original(callback)
}

func (b *blocker) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("transaction was not entered")
	}
}

// waitFor polls until condition holds, as advanceTimersByTimeAsync lets pending work run.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second) //portlint:allow clock test deadline, not recorded time
	for !condition() {
		if time.Now().After(deadline) { //portlint:allow clock test deadline, not recorded time
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func rowNumberOf(t *testing.T, row chordsqlite.SqliteRow, column string) float64 {
	t.Helper()
	number, err := rowNumber(row, column)
	check(t, err)
	return number
}

// fakeTicks is a fake one-second timer the test fires by hand, as vi.advanceTimersByTimeAsync(1000) does.
type fakeTicks struct {
	channel chan time.Time
	stopped atomic.Bool
}

func newFakeTicks() *fakeTicks { return &fakeTicks{channel: make(chan time.Time)} }

func (ticks *fakeTicks) options(options SqliteTelemetryOptions) SqliteTelemetryOptions {
	options.ticks = func() (<-chan time.Time, func()) {
		return ticks.channel, func() { ticks.stopped.Store(true) }
	}
	return options
}

// fire delivers one tick and returns whether the timer goroutine took it.
func (ticks *fakeTicks) fire(wait time.Duration) bool {
	select {
	case ticks.channel <- time.Time{}:
		return true
	case <-time.After(wait):
		return false
	}
}

func openMemoryDatabase() (chordsqlite.SqliteDatabase, error) {
	return node.OpenNodeSqliteDatabase(":memory:", node.NodeSqliteDatabaseOptions{})
}

func slicesSortSpans(spans []telemetry.RecordedTelemetrySpan) {
	slices.SortFunc(spans, func(a, b telemetry.RecordedTelemetrySpan) int { return a.ID - b.ID })
}
