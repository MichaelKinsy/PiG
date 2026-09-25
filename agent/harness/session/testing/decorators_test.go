package sessiontesting_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	sessiontesting "github.com/MichaelKinsy/PiG/agent/harness/session/testing"
	"github.com/MichaelKinsy/PiG/ai"
)

var background = context.Background()

func fixedClock(now int64) *session.MemoryStorageOptions {
	return &session.MemoryStorageOptions{Now: func() int64 { return now }}
}

func setName(name string) []session.Write {
	return []session.Write{session.SetValue(session.SessionName, name)}
}

func storedName(t *testing.T, storage session.Storage) string {
	t.Helper()
	stored, err := storage.GetValue(background, session.SessionName.StoredAddressBase)
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil {
		return ""
	}
	return stored.Value.(string)
}

// controlledCommitStorage parks every commit until the test settles it.
type controlledCommitStorage struct {
	*session.MemoryStorage
	mu      sync.Mutex
	pending []chan commitOutcome
	admits  chan struct{}
}

type commitOutcome struct {
	result session.CommitResult
	err    error
}

func newControlledCommitStorage() *controlledCommitStorage {
	return &controlledCommitStorage{MemoryStorage: session.NewMemoryStorage(nil), admits: make(chan struct{}, 16)}
}

func (storage *controlledCommitStorage) Commit(context.Context, []session.Write) (session.CommitResult, error) {
	settle := make(chan commitOutcome, 1)
	storage.mu.Lock()
	storage.pending = append(storage.pending, settle)
	storage.mu.Unlock()
	storage.admits <- struct{}{}
	outcome := <-settle
	return outcome.result, outcome.err
}

func (storage *controlledCommitStorage) resolveNext(result session.CommitResult) {
	storage.mu.Lock()
	settle := storage.pending[0]
	storage.pending = storage.pending[1:]
	storage.mu.Unlock()
	settle <- commitOutcome{result: result}
}

func goCommit(storage session.Storage, writes []session.Write) <-chan commitOutcome {
	done := make(chan commitOutcome, 1)
	go func() {
		result, err := storage.Commit(background, writes)
		done <- commitOutcome{result, err}
	}()
	return done
}

func TestInstrumentedStorageRecordsAttemptsInAdmissionOrderBeforeSettlement(t *testing.T) {
	delegate := newControlledCommitStorage()
	storage := sessiontesting.NewInstrumentedStorage(delegate)
	first, second := setName("first"), setName("second")

	firstCommit := goCommit(storage, first)
	<-delegate.admits
	if attempts := storage.GetCommitAttempts(); len(attempts) != 1 || &attempts[0][0] != &first[0] {
		t.Fatalf("attempts after first = %v", attempts)
	}
	secondCommit := goCommit(storage, second)
	<-delegate.admits
	if attempts := storage.GetCommitAttempts(); len(attempts) != 2 || &attempts[1][0] != &second[0] {
		t.Fatalf("attempts after second = %v", attempts)
	}

	firstResult := session.CommitResult{FirstSeq: 1, Seqs: []int64{1}, Timestamp: 10}
	delegate.resolveNext(firstResult)
	if outcome := <-firstCommit; outcome.err != nil || !reflect.DeepEqual(outcome.result, firstResult) {
		t.Fatalf("first commit = %+v", outcome)
	}
	if len(storage.GetCommitAttempts()) != 2 {
		t.Fatal("settlement changed recorded attempts")
	}
	secondResult := session.CommitResult{FirstSeq: 2, Seqs: []int64{2}, Timestamp: 20}
	delegate.resolveNext(secondResult)
	if outcome := <-secondCommit; outcome.err != nil || !reflect.DeepEqual(outcome.result, secondResult) {
		t.Fatalf("second commit = %+v", outcome)
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestInstrumentedStorageRecordsTheTransactionReferencePassedToTheDelegate(t *testing.T) {
	delegate := newControlledCommitStorage()
	storage := sessiontesting.NewInstrumentedStorage(delegate)
	transaction := setName("value")
	commit := goCommit(storage, transaction)
	<-delegate.admits
	if recorded := storage.GetCommitAttempts()[0]; &recorded[0] != &transaction[0] {
		t.Fatal("recorded a copy instead of the transaction reference")
	}
	delegate.resolveNext(session.CommitResult{FirstSeq: 1, Seqs: []int64{1}, Timestamp: 10})
	<-commit
}

func TestInstrumentedStorageClearsAttemptsWithoutAffectingTheDelegate(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(session.NewMemoryStorage(fixedClock(100)))
	if _, err := storage.Commit(background, setName("first")); err != nil {
		t.Fatal(err)
	}
	storage.ClearCommitAttempts()
	if len(storage.GetCommitAttempts()) != 0 || storedName(t, storage) != "first" {
		t.Fatalf("clear affected state: %v", storage.GetCommitAttempts())
	}
	second := setName("second")
	if _, err := storage.Commit(background, second); err != nil {
		t.Fatal(err)
	}
	if attempts := storage.GetCommitAttempts(); len(attempts) != 1 || &attempts[0][0] != &second[0] {
		t.Fatalf("attempts = %v", attempts)
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func assertSameJSON(t *testing.T, label string, got, want any) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("%s\n got: %s\nwant: %s", label, gotJSON, wantJSON)
	}
}

func TestInstrumentedStorageDelegatesEveryReadWithoutRecordingSyntheticWrites(t *testing.T) {
	delegate := session.NewMemoryStorage(fixedClock(100))
	storage := sessiontesting.NewInstrumentedStorage(delegate)
	events := session.MustList[string]("test.events", "")
	if _, err := storage.Commit(background, []session.Write{
		session.InsertEntry(session.Entry{ID: "root", Type: session.EntryTypeCustom, CustomType: "note"}),
		session.SetValue(session.SessionName, "session"),
		session.AppendList(events, "event"),
		session.InsertUsage(session.UsageRow{ID: "usage", Usage: ai.Usage{Input: 1, Output: 2, TotalTokens: 3}}),
	}); err != nil {
		t.Fatal(err)
	}
	type read func(session.Storage) (any, error)
	reads := map[string]read{
		"getEntries": func(s session.Storage) (any, error) { return s.GetEntries(background, []string{"root"}) },
		"getValue": func(s session.Storage) (any, error) {
			return s.GetValue(background, session.SessionName.StoredAddressBase)
		},
		"scanValues": func(s session.Storage) (any, error) {
			return s.ScanValues(background, session.SessionName.StoredAddressBase)
		},
		"readList": func(s session.Storage) (any, error) { return s.ReadList(background, events.StoredAddressBase, nil) },
		"scanBranch": func(s session.Storage) (any, error) {
			return s.ScanBranch(background, session.StorageBranchScan{Start: "root"})
		},
		"scanBranchStructure": func(s session.Storage) (any, error) {
			return s.ScanBranchStructure(background, session.StorageBranchScan{Start: "root"})
		},
		"scanEntries": func(s session.Storage) (any, error) {
			return s.ScanEntries(background, session.EntryScan{Order: session.OrderAsc})
		},
		"scanUsage": func(s session.Storage) (any, error) {
			return s.ScanUsage(background, session.UsageScan{Order: session.OrderAsc})
		},
		"getStats": func(s session.Storage) (any, error) { return s.GetStats(background) },
	}
	for name, readFn := range reads {
		got, err := readFn(storage)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want, err := readFn(delegate)
		if err != nil {
			t.Fatalf("%s delegate: %v", name, err)
		}
		assertSameJSON(t, name, got, want)
	}
	if len(storage.GetCommitAttempts()) != 1 {
		t.Fatalf("reads recorded writes: %d", len(storage.GetCommitAttempts()))
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

type readListSpy struct {
	*session.MemoryStorage
	reads atomic.Int32
}

func (spy *readListSpy) ReadList(ctx context.Context, address session.StoredAddressBase, options *session.ListReadOptions) ([]session.ListElement[any], error) {
	spy.reads.Add(1)
	return spy.MemoryStorage.ReadList(ctx, address, options)
}

func TestInstrumentedStorageRecordsListAppendsWithoutReadingTheTargetList(t *testing.T) {
	delegate := &readListSpy{MemoryStorage: session.NewMemoryStorage(fixedClock(100))}
	storage := sessiontesting.NewInstrumentedStorage(delegate)
	events := session.MustList[string]("test.events", "")
	if _, err := storage.Commit(background, []session.Write{session.AppendList(events, "event")}); err != nil {
		t.Fatal(err)
	}
	if delegate.reads.Load() != 0 {
		t.Fatal("append read the target list")
	}
	encoded, err := json.Marshal(storage.GetCommitAttempts())
	if err != nil || string(encoded) != `[[{"kind":"list","op":"append","namespace":"test.events","key":"","value":"event"}]]` {
		t.Fatalf("attempts = %s, %v", encoded, err)
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestInstrumentedStorageDelegatesCloseIdempotenceAndAdmittedCommitDraining(t *testing.T) {
	storage := sessiontesting.NewInstrumentedStorage(session.NewMemoryStorage(fixedClock(100)))
	admitted := goCommit(storage, setName("admitted"))
	var closes sync.WaitGroup
	for range 2 {
		closes.Go(func() {
			if err := storage.Close(background); err != nil {
				t.Error(err)
			}
		})
	}
	// A goroutine's commit may reach admission after a concurrent close, which
	// a synchronous JavaScript call cannot; such a commit is rejected, never
	// half-applied.
	if outcome := <-admitted; outcome.err != nil && outcome.err.Error() != "MemoryStorage is closed" {
		t.Fatalf("admitted commit = %v", outcome.err)
	}
	closes.Wait()
	if _, err := storage.GetStats(background); err == nil || err.Error() != "MemoryStorage is closed" {
		t.Fatalf("stats after close err = %v", err)
	}
	if len(storage.GetCommitAttempts()) != 1 {
		t.Fatalf("attempts = %d", len(storage.GetCommitAttempts()))
	}
}

// landingStorage blocks each commit until released so a test can observe the
// gap between release and landing.
type landingStorage struct {
	*session.MemoryStorage
	started chan struct{}
	release chan struct{}
}

func (storage *landingStorage) Commit(ctx context.Context, writes []session.Write) (session.CommitResult, error) {
	storage.started <- struct{}{}
	<-storage.release
	return storage.MemoryStorage.Commit(ctx, writes)
}

func TestGatingStorageBypassesSetupUntilArmedAndWaitsForParkedCommits(t *testing.T) {
	storage := sessiontesting.NewGatingStorage(session.NewMemoryStorage(fixedClock(10)))
	if _, err := storage.Commit(background, setName("setup")); err != nil {
		t.Fatal(err)
	}
	if storage.Pending() != 0 {
		t.Fatal("setup commit parked before arm")
	}
	storage.Arm()
	waiting := make(chan error, 1)
	go func() { waiting <- storage.WaitPending(1) }()
	select {
	case <-waiting:
		t.Fatal("WaitPending resolved before a commit parked")
	case <-time.After(20 * time.Millisecond):
	}
	commit := goCommit(storage, setName("parked"))
	if err := <-waiting; err != nil {
		t.Fatal(err)
	}
	if storage.Pending() != 1 || storedName(t, storage) != "setup" {
		t.Fatalf("pending=%d name=%q", storage.Pending(), storedName(t, storage))
	}
	if err := storage.Next(1); err != nil {
		t.Fatal(err)
	}
	if outcome := <-commit; outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if storage.Pending() != 0 || storedName(t, storage) != "parked" {
		t.Fatalf("pending=%d name=%q", storage.Pending(), storedName(t, storage))
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestGatingStorageReleasesFIFOAndNextResolvesOnlyAfterTheWriteLands(t *testing.T) {
	delegate := &landingStorage{MemoryStorage: session.NewMemoryStorage(fixedClock(10)), started: make(chan struct{}, 2), release: make(chan struct{}, 2)}
	storage := sessiontesting.NewGatingStorage(delegate)
	storage.Arm()
	first := goCommit(storage, setName("first"))
	if err := storage.WaitPending(1); err != nil {
		t.Fatal(err)
	}
	second := goCommit(storage, setName("second"))
	if err := storage.WaitPending(2); err != nil {
		t.Fatal(err)
	}
	next := make(chan error, 1)
	go func() { next <- storage.Next(1) }()
	<-delegate.started
	select {
	case <-next:
		t.Fatal("Next resolved before the released write landed")
	case <-time.After(20 * time.Millisecond):
	}
	if storage.Pending() != 1 {
		t.Fatalf("pending = %d", storage.Pending())
	}
	delegate.release <- struct{}{}
	if err := <-next; err != nil {
		t.Fatal(err)
	}
	if outcome := <-first; outcome.err != nil || storedName(t, storage) != "first" {
		t.Fatalf("first = %+v name=%q", outcome, storedName(t, storage))
	}
	delegate.release <- struct{}{}
	if err := storage.Next(1); err != nil {
		t.Fatal(err)
	}
	<-delegate.started
	if outcome := <-second; outcome.err != nil || storedName(t, storage) != "second" {
		t.Fatalf("second = %+v name=%q", outcome, storedName(t, storage))
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestGatingStoragePermanentlyRejectsAfterDiscard(t *testing.T) {
	storage := sessiontesting.NewGatingStorage(session.NewMemoryStorage(nil))
	storage.Arm()
	waiting := make(chan error, 1)
	go func() { waiting <- storage.WaitPending(2) }()
	parked := goCommit(storage, setName("lost"))
	if err := storage.WaitPending(1); err != nil {
		t.Fatal(err)
	}
	storage.Discard()
	if storage.Pending() != 0 {
		t.Fatalf("pending = %d", storage.Pending())
	}
	var discarded *sessiontesting.CommitDiscarded
	if outcome := <-parked; !errors.As(outcome.err, &discarded) {
		t.Fatalf("parked err = %v", outcome.err)
	}
	if err := <-waiting; !errors.As(err, &discarded) {
		t.Fatalf("waiting err = %v", err)
	}
	if err := storage.Next(1); !errors.As(err, &discarded) {
		t.Fatalf("next err = %v", err)
	}
	if _, err := storage.Commit(background, nil); !errors.As(err, &discarded) {
		t.Fatalf("later commit err = %v", err)
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
	unarmed := sessiontesting.NewGatingStorage(session.NewMemoryStorage(nil))
	unarmed.Discard()
	if _, err := unarmed.Commit(background, nil); !errors.As(err, &discarded) {
		t.Fatalf("unarmed err = %v", err)
	}
	if err := unarmed.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestGatingStorageRecordsAttemptsBeforeGatingParksThem(t *testing.T) {
	gating := sessiontesting.NewGatingStorage(session.NewMemoryStorage(nil))
	storage := sessiontesting.NewInstrumentedStorage(gating)
	gating.Arm()
	writes := setName("recorded")
	commit := goCommit(storage, writes)
	if err := gating.WaitPending(1); err != nil {
		t.Fatal(err)
	}
	if attempts := storage.GetCommitAttempts(); len(attempts) != 1 || &attempts[0][0] != &writes[0] {
		t.Fatalf("attempts = %v", attempts)
	}
	if gating.Pending() != 1 {
		t.Fatalf("pending = %d", gating.Pending())
	}
	if err := gating.Next(1); err != nil {
		t.Fatal(err)
	}
	if outcome := <-commit; outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if err := storage.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestGatingStorageRejectsNonPositiveCounts(t *testing.T) {
	storage := sessiontesting.NewGatingStorage(session.NewMemoryStorage(nil))
	if err := storage.WaitPending(0); err == nil || err.Error() != "Pending commit count must be a positive safe integer" {
		t.Fatalf("wait err = %v", err)
	}
	if err := storage.Next(0); err == nil || err.Error() != "Released commit count must be a positive safe integer" {
		t.Fatalf("next err = %v", err)
	}
}
