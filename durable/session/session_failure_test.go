package session_test

// Ports the Session-level cases of packages/durable/test/harness-storage-failure.test.ts and session.ts GuardedStorage:
// a Storage call that fails fails the Session, which ends everything with that first error.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// scriptedStorage fails or holds the calls a test names; every other call reaches memory.
type scriptedStorage struct {
	*storage.MemoryStorage
	mu       sync.Mutex
	taskErr  error
	taskHold chan struct{}
	taskIn   chan struct{}
	closeErr error
	closed   chan struct{}
	// reading counts the Task and FindDocument calls underway; closedWhileReading is set when Close found any.
	reading            int
	closedWhileReading bool
	findHold, findIn   chan struct{}
}

func newScripted() *scriptedStorage {
	return &scriptedStorage{MemoryStorage: storage.NewMemoryStorage(), closed: make(chan struct{})}
}

func (scripted *scriptedStorage) Task(callCtx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	scripted.mu.Lock()
	err, hold, in := scripted.taskErr, scripted.taskHold, scripted.taskIn
	scripted.taskErr, scripted.taskHold, scripted.taskIn = nil, nil, nil
	scripted.reading++
	scripted.mu.Unlock()
	defer func() {
		scripted.mu.Lock()
		scripted.reading--
		scripted.mu.Unlock()
	}()
	if in != nil {
		close(in)
	}
	if hold != nil {
		<-hold
	}
	if err != nil {
		return nil, err
	}
	return scripted.MemoryStorage.Task(callCtx, id)
}

func (scripted *scriptedStorage) FindDocument(callCtx context.Context, address durable.DocumentAddress, at durable.DocumentPoint) (*durable.DocumentRecord, error) {
	scripted.mu.Lock()
	hold, in := scripted.findHold, scripted.findIn
	scripted.findHold, scripted.findIn = nil, nil
	scripted.reading++
	scripted.mu.Unlock()
	defer func() {
		scripted.mu.Lock()
		scripted.reading--
		scripted.mu.Unlock()
	}()
	if in != nil {
		close(in)
	}
	if hold != nil {
		<-hold
	}
	return scripted.MemoryStorage.FindDocument(callCtx, address, at)
}

func (scripted *scriptedStorage) Close(callCtx context.Context) error {
	scripted.mu.Lock()
	scripted.closedWhileReading = scripted.reading > 0
	scripted.mu.Unlock()
	close(scripted.closed)
	if err := scripted.MemoryStorage.Close(callCtx); err != nil {
		return err
	}
	return scripted.closeErr
}

type reportLog struct {
	mu   sync.Mutex
	errs []error
}

func (log *reportLog) add(err error) {
	log.mu.Lock()
	log.errs = append(log.errs, err)
	log.mu.Unlock()
}

func (log *reportLog) all() []error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]error(nil), log.errs...)
}

func openScripted(t *testing.T) (*session.SessionImpl, *scriptedStorage, *reportLog) {
	t.Helper()
	scripted := newScripted()
	reports := &reportLog{}
	kernel := session.NewSessionImpl(scripted, session.Hooks{Report: func(_ context.Context, err error, _ durable.ReportInfo) { reports.add(err) }}, nil, nil)
	return kernel, scripted, reports
}

// readTask reads a task through a commit, so the read goes through the Session's guarded Storage.
func readGuardedTask(kernel *session.SessionImpl, callCtx context.Context) error {
	_, err := kernel.CommitWith(callCtx, func(tx *session.Transaction) (any, error) { return tx.Task(1) }, session.TransactionScope{})
	return err
}

func TestAFailedStorageCallFailsTheSession(t *testing.T) {
	t.Run("ends everything with the first error: the call, later calls, close listeners, the end", func(t *testing.T) {
		kernel, scripted, reports := openScripted(t)
		disk := errors.New("disk gone")
		scripted.taskErr = disk
		closedBy := make(chan error, 1)
		kernel.SubscribeClose(func() { closedBy <- kernel.Failure() })
		// The call that hit it gets the storage error itself.
		if err := readGuardedTask(kernel, ctx); !errors.Is(err, disk) {
			t.Fatalf("the failing call returned %v, want the storage error", err)
		}
		if got := <-closedBy; !errors.Is(got, disk) {
			t.Fatalf("the close listener saw failure %v, want the storage error", got)
		}
		select {
		case <-kernel.Failed():
		default:
			t.Fatal("Failed is not closed after the failure")
		}
		expectSessionFailed(t, readGuardedTask(kernel, ctx), "disk gone")
		expectSessionFailed(t, tryCommit(kernel, func(durable.Tx) error { return nil }), "disk gone")
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, disk) {
			t.Fatalf("end = %+v, want failed with the first error", end)
		}
		if reported := reports.all(); len(reported) != 1 || !errors.Is(reported[0], disk) {
			t.Fatalf("reports = %v, want the first error once", reported)
		}
		// A second failure does not replace the first.
		kernel.Fail(context.Background(), errors.New("later"))
		if !errors.Is(kernel.Failure(), disk) || len(reports.all()) != 1 {
			t.Fatalf("failure %v, reports %v: the first error stays", kernel.Failure(), reports.all())
		}
		must(t, kernel.Close(ctx))
	})

	t.Run("a StorageRequestError, or a read its caller cancelled, fails only that read", func(t *testing.T) {
		kernel, scripted, reports := openScripted(t)
		scripted.taskErr = durable.NewStorageRequestError("Unknown conversation: 9")
		if _, ok := errors.AsType[*durable.StorageRequestError](readGuardedTask(kernel, ctx)); !ok {
			t.Fatal("the read did not return its StorageRequestError")
		}
		cancelled, cancel := context.WithCancel(ctx)
		scripted.taskErr = errors.New("cancelled mid-read")
		hold, entered := make(chan struct{}), make(chan struct{})
		scripted.taskHold, scripted.taskIn = hold, entered
		reading := make(chan error, 1)
		go func() { reading <- readGuardedTask(kernel, cancelled) }()
		<-entered
		cancel()
		close(hold)
		if err := <-reading; err == nil {
			t.Fatal("the cancelled read succeeded")
		}
		if kernel.Failure() != nil || len(reports.all()) != 0 {
			t.Fatalf("the Session failed (%v, %v) for a read that says nothing about Storage", kernel.Failure(), reports.all())
		}
		must(t, readGuardedTask(kernel, ctx))
		must(t, kernel.Close(ctx))
	})

	t.Run("a commit's error is never exempt, whatever it is", func(t *testing.T) {
		harness := open()
		request := durable.NewStorageRequestError("rejected")
		harness.Storage.FailNextCommit(request)
		err := tryCommit(harness.Session, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(ownerless())
			return err
		})
		if !errors.Is(err, request) {
			t.Fatalf("commit error %v, want the storage error", err)
		}
		expectSessionFailed(t, tryCommit(harness.Session, func(durable.Tx) error { return nil }), "rejected")
		must(t, harness.Session.Close(ctx))
	})

	t.Run("a call underway when another fails the Session ends with SessionFailed, and close waits for it", func(t *testing.T) {
		kernel, scripted, _ := openScripted(t)
		hold, entered := make(chan struct{}), make(chan struct{})
		scripted.findHold, scripted.findIn = hold, entered
		reading := make(chan error, 1)
		go func() {
			// Off the Session line, as the context read of a Harness is.
			_, err := kernel.Storage().FindDocument(ctx, durable.DocumentAddress{}, durable.CurrentPoint)
			reading <- err
		}()
		<-entered
		// Another call fails the Session while the read is underway.
		disk := errors.New("disk gone")
		kernel.Fail(context.Background(), disk)
		for range 200 {
			select {
			case <-scripted.closed:
			default:
				runtime.Gosched()
				continue
			}
			break
		}
		select {
		case <-kernel.Closed():
			t.Fatal("the Session ended while a Storage call was underway")
		default:
		}
		// The read succeeds at the backend, yet its caller gets the failure: nothing it read is used.
		close(hold)
		expectSessionFailed(t, <-reading, "disk gone")
		<-kernel.Closed()
		scripted.mu.Lock()
		early := scripted.closedWhileReading
		scripted.mu.Unlock()
		if early {
			t.Fatal("the backend closed while a Storage call was underway")
		}
		if end := kernel.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, disk) {
			t.Fatalf("end = %+v", end)
		}
	})

	t.Run("a callback that caught a failed read cannot commit, nor succeed as if it had", func(t *testing.T) {
		kernel, scripted, _ := openScripted(t)
		scripted.taskErr = errors.New("disk gone")
		_, err := kernel.CommitWith(ctx, func(tx *session.Transaction) (any, error) {
			_, _ = tx.Task(1) // the error is swallowed, and nothing is written
			return nil, nil
		}, session.TransactionScope{})
		expectSessionFailed(t, err, "disk gone")
	})

	t.Run("fails it when Storage cannot close; an earlier failure stays the cause", func(t *testing.T) {
		kernel, scripted, reports := openScripted(t)
		closeErr := errors.New("close failed")
		scripted.closeErr = closeErr
		if err := kernel.Close(ctx); !errors.Is(err, closeErr) {
			t.Fatalf("close returned %v, want the close error", err)
		}
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndFailed || !errors.Is(end.Error, closeErr) {
			t.Fatalf("end = %+v, want failed with the close error", end)
		}
		if reported := reports.all(); len(reported) != 1 || !errors.Is(reported[0], closeErr) {
			t.Fatalf("reports = %v", reported)
		}
		earlier, scriptedEarlier, _ := openScripted(t)
		first := errors.New("disk gone")
		scriptedEarlier.closeErr = closeErr
		earlier.Fail(context.Background(), first)
		<-earlier.Closed()
		if end := earlier.End(); !errors.Is(end.Error, first) {
			t.Fatalf("end = %+v, want the first failure as the cause", end)
		}
	})

	t.Run("closes cleanly with the closed end", func(t *testing.T) {
		kernel, _, reports := openScripted(t)
		must(t, kernel.Close(ctx))
		<-kernel.Closed()
		if end := kernel.End(); end.Reason != durable.SessionEndClosed || end.Error != nil {
			t.Fatalf("end = %+v, want closed", end)
		}
		if len(reports.all()) != 0 {
			t.Fatalf("reports = %v", reports.all())
		}
	})
}

func TestListenersOfAFailingPublication(t *testing.T) {
	commitOne := func(harness func() error) error { return harness() }
	t.Run("an internal listener's failure fails the Session; the host's still run", func(t *testing.T) {
		kernel, _, reports := openScripted(t)
		var host int
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { host++ })
		kernel.ObserveCommits(func(context.Context, durable.CommitPublication) { panic(errors.New("bookkeeping failed")) })
		err := commitOne(func() error {
			return tryCommit(kernel, func(tx durable.Tx) error {
				_, err := tx.CreateConversation(ownerless())
				return err
			})
		})
		if err != nil {
			t.Fatalf("the commit is durable: a failing internal listener does not fail it: %v", err)
		}
		if host != 1 {
			t.Fatalf("host listener ran %d times, want 1", host)
		}
		if failure := kernel.Failure(); failure == nil || failure.Error() != "bookkeeping failed" {
			t.Fatalf("failure = %v, want the listener's error", failure)
		}
		if reported := reports.all(); len(reported) != 1 {
			t.Fatalf("reports = %v", reported)
		}
		expectSessionFailed(t, tryCommit(kernel, func(durable.Tx) error { return nil }), "bookkeeping failed")
	})

	t.Run("a host listener's panic is reported and the others still run; the Session stays healthy", func(t *testing.T) {
		kernel, _, reports := openScripted(t)
		var after int
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { panic("listener failed") })
		kernel.SubscribeCommits(func(context.Context, durable.CommitPublication) { after++ })
		must(t, tryCommit(kernel, func(tx durable.Tx) error {
			_, err := tx.CreateConversation(ownerless())
			return err
		}))
		if after != 1 || kernel.Failure() != nil {
			t.Fatalf("later listener ran %d times, failure %v", after, kernel.Failure())
		}
		if reported := reports.all(); len(reported) != 1 || reported[0].Error() != "listener failed" {
			t.Fatalf("reports = %v", reported)
		}
	})
}
