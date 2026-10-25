// Ports packages/durable/test/harness-storage-failure.test.ts (Pi 1.2.0) "a failed Storage call"; the "listener and report
// errors" group is harness_listener_errors_test.go.

package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type signalLog struct {
	mu      sync.Mutex
	seen    []string
	started chan struct{}
	once    sync.Once
}

// startedSignal is closed once the handler of a waitingTask has started.
func (log *signalLog) startedSignal() chan struct{} {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.started == nil {
		log.started = make(chan struct{})
	}
	return log.started
}

// begun reports that the handler of a waitingTask is running, and about to wait for its signal.
func (log *signalLog) begun() {
	started := log.startedSignal()
	log.once.Do(func() { close(started) })
}

func (log *signalLog) add(entry string) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.seen = append(log.seen, entry)
}

func (log *signalLog) all() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.seen...)
}

// waitingTask waits for its signal, records that it saw it, and ends aborted through its abort handler.
func waitingTask(log *signalLog) durable.Task[struct{}, stepState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[struct{}, stepState, durable.JsonValue, any]{
		Name:    "test.waiting",
		Version: 1,
		Initial: func(struct{}) stepState { return stepState{Phase: "work"} },
		Phases: map[string]durable.PhaseHandler[struct{}, stepState, durable.JsonValue, any]{
			"work": func(_ context.Context, _ durable.RunningTask[struct{}, stepState, durable.JsonValue], runtime durable.TaskRuntime[struct{}, stepState, durable.JsonValue, any]) error {
				log.begun()
				_ = abortedBy(runtime.Signal())
				log.add("aborted")
				return nil
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[struct{}, stepState, durable.JsonValue], runtime durable.TaskRuntime[struct{}, stepState, durable.JsonValue, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[struct{}, stepState, durable.JsonValue]) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue](""), nil
			})
		},
	})
}

// failingCalls fails the Storage methods named in failing once with err. A scan of entries with a limit above 1 is the
// context read's scan of its range, off the Session line; heldRange holds it until released.
type failingCalls struct {
	*controlledStorage
	err error

	mu        sync.Mutex
	failing   map[string]bool
	heldRange *gate
	closeErr  error
	pages     []string

	// cancelRead, once set, cancels the caller of the next context read as its scan starts, and fails the scan.
	cancelRead  func()
	cancelCause error
}

func (s *failingCalls) failTask() { s.add("task") }

func (s *failingCalls) Task(ctx context.Context, id durable.TaskId) (*durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue], error) {
	if s.fail("task") {
		return nil, s.err
	}
	return s.controlledStorage.Task(ctx, id)
}

func newFailingCalls() *failingCalls {
	return &failingCalls{controlledStorage: newControlledStorage(), err: errors.New("disk gone"), failing: map[string]bool{}}
}

func (s *failingCalls) fail(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	was := s.failing[method]
	delete(s.failing, method)
	return was
}

func (s *failingCalls) add(method string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing[method] = true
}

func (s *failingCalls) ScanEntries(ctx context.Context, query durable.EntryQuery, limit int, cursor durable.Cursor) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
	if limit > 1 {
		s.mu.Lock()
		held, cancel := s.heldRange, s.cancelRead
		s.cancelRead = nil
		if cursor == nil {
			s.pages = append(s.pages, "first")
		} else {
			s.pages = append(s.pages, "next")
		}
		s.mu.Unlock()
		if cancel != nil {
			cancel()
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, s.cancelCause
		}
		if held != nil {
			held.entered.resolve()
			_ = held.held.wait(testContext)
		}
		if s.fail("scanRange") {
			return durable.Page[durable.EntryRecord, durable.Cursor]{}, s.err
		}
	}
	return s.controlledStorage.ScanEntries(ctx, query, limit, cursor)
}

func (s *failingCalls) MintId() (int64, error) {
	if s.fail("mintId") {
		return 0, s.err
	}
	return s.controlledStorage.MintId()
}

func (s *failingCalls) Close(ctx context.Context) error {
	s.mu.Lock()
	s.pages = append(s.pages, "close")
	s.mu.Unlock()
	err := s.controlledStorage.Close(ctx)
	if s.closeErr != nil {
		return s.closeErr
	}
	return err
}

func sfStarted(t *testing.T, store durable.Storage, log *signalLog) (Harness, Conversation, *reportLog) {
	t.Helper()
	harness, _, reports := openTasks(t, store, []durable.AnyTask{waitingTask(log).Erased()})
	return harness, mustRoot(t, harness, nil), reports
}

func sfAppendNote(root Conversation) error {
	_, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
		return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
	})
	return err
}

func sfSessionFailed(t *testing.T, err error, cause error) {
	t.Helper()
	failed, ok := errors.AsType[*durable.SessionFailed](err)
	if !ok || !errors.Is(failed, cause) {
		t.Fatalf("error %v, want SessionFailed caused by %v", err, cause)
	}
}

func sfEnded(t *testing.T, harness Harness, reason durable.SessionEndReason, cause error) {
	t.Helper()
	select {
	case <-harness.Closed():
	case <-time.After(10 * time.Second):
		t.Fatal("the Harness did not end")
	}
	if end := harness.End(); end.Reason != reason || !errors.Is(end.Error, cause) || (cause == nil && end.Error != nil) {
		t.Fatalf("end %+v, want %s caused by %v", end, reason, cause)
	}
}

func isStorageRequestError(err error) bool {
	_, ok := errors.AsType[*durable.StorageRequestError](err)
	return ok
}

func sfReports(t *testing.T, reports *reportLog, want ...error) {
	t.Helper()
	got := reports.all()
	if len(got) != len(want) {
		t.Fatalf("reports %v, want %v", got, want)
	}
	for i := range want {
		if !errors.Is(got[i], want[i]) {
			t.Fatalf("report %d is %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAFailedStorageCall(t *testing.T) {
	// harness-storage-failure.test.ts:86 it("ends everything with the first error: the call, later calls, waits, watches, streams, running tasks")
	t.Run("ends everything with the first error: the call, later calls, waits, watches, streams, running tasks", func(t *testing.T) {
		log := &signalLog{}
		store := newControlledStorage()
		harness, root, reports := sfStarted(t, store, log)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, waitingTask(log), struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}))
		harness.Resume()
		eventually(t, func() bool {
			record, _ := harness.GetTask(testContext, id)
			return record != nil && record.State.Status == durable.TaskRunning
		})
		// The task is running once that is committed, which is before its handler is dispatched, so a storage failure that landed now could end the Session
		// first and the handler would never log. Pi's handler is awaiting its signal by the time `running` resolves (microtask hops); this one starts on
		// its own goroutine, so the test waits for it to start.
		select {
		case <-log.startedSignal():
		case <-time.After(10 * time.Second):
			t.Fatal("the task's handler did not start")
		}
		task, idle := make(chan error, 1), make(chan error, 1)
		go func() { _, err := harness.WaitForTask(testContext, id); task <- err }()
		go func() { idle <- harness.WaitForIdle(testContext) }()
		watch := must(harness.WatchDocErased(testContext, LiveDoc, root.Id()))
		stream := must(WatchEvents(testContext, harness, root.Id()))
		stream.Start(func(context.Context, []AgentEvent) error { return nil })
		flush()

		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		// The call that hit it gets the storage error itself.
		if err := sfAppendNote(root); !errors.Is(err, failure) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		sfSessionFailed(t, <-task, failure)
		sfSessionFailed(t, <-idle, failure)
		<-watch.Closed()
		if end := watch.End(); end.Reason != durable.WatchFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("watch ended %+v, want failed with the first error", end)
		}
		<-stream.Closed()
		if end := stream.End(); end.Reason != durable.WatchFailed || !errors.Is(end.Error, failure) {
			t.Fatalf("stream ended %+v, want failed with the first error", end)
		}
		// Every later call, and a wait that starts now.
		_, err := harness.GetTask(testContext, id)
		sfSessionFailed(t, err, failure)
		sfSessionFailed(t, harness.WaitForIdle(testContext), failure)
		_, err = root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("x")})
		sfSessionFailed(t, err, failure)
		sfEnded(t, harness, durable.SessionEndFailed, failure)
		if got := log.all(); len(got) != 1 || got[0] != "aborted" {
			t.Fatalf("signals %v, want [aborted]", got)
		}
		sfReports(t, reports, failure)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:121 it("fails it from a read off the Session line, as a context read makes")
	t.Run("fails it from a read off the Session line, as a context read makes", func(t *testing.T) {
		store := newFailingCalls()
		harness, root, reports := sfStarted(t, store, &signalLog{})
		if err := sfAppendNote(root); err != nil {
			t.Fatal(err)
		}
		store.add("scanRange")
		if _, err := root.Context(testContext, nil); !errors.Is(err, store.err) {
			t.Fatalf("context read returned %v, want the storage error", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, store.err)
		sfReports(t, reports, store.err)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:132 it("ends Storage calls underway with SessionFailed, and closes once they have settled")
	t.Run("ends Storage calls underway with SessionFailed, and closes once they have settled", func(t *testing.T) {
		store := newFailingCalls()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		if err := sfAppendNote(root); err != nil {
			t.Fatal(err)
		}
		held := gate{entered: deferred(), held: deferred()}
		store.mu.Lock()
		store.heldRange = &held
		store.mu.Unlock()
		reading := make(chan error, 1)
		go func() { _, err := root.Context(testContext, nil); reading <- err }()
		_ = held.entered.wait(testContext)
		// Another call fails the Session while the context read is underway.
		store.add("mintId")
		if err := sfAppendNote(root); !errors.Is(err, store.err) {
			t.Fatalf("the failing commit returned %v, want the storage error", err)
		}
		time.Sleep(50 * time.Millisecond)
		select {
		case <-harness.Closed():
			t.Fatal("the Session closed while a Storage call was underway")
		default:
		}
		// The read succeeds at the backend, yet its caller gets the failure: nothing it read is used.
		held.release()
		sfSessionFailed(t, <-reading, store.err)
		sfEnded(t, harness, durable.SessionEndFailed, store.err)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:156 it("closes the backend only after a paged read underway, which cannot start another page")
	t.Run("closes the backend only after a paged read underway, which cannot start another page", func(t *testing.T) {
		store := newFailingCalls()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		// More entries than one page of the context read.
		for range 300 {
			if err := sfAppendNote(root); err != nil {
				t.Fatal(err)
			}
		}
		store.mu.Lock()
		store.pages = nil
		held := gate{entered: deferred(), held: deferred()}
		store.heldRange = &held
		store.mu.Unlock()
		reading := make(chan error, 1)
		go func() { _, err := root.Context(testContext, nil); reading <- err }()
		_ = held.entered.wait(testContext)
		closing := make(chan error, 1)
		go func() { closing <- harness.Close(testContext) }()
		// In Pi the guarded Storage refuses new calls (session.ts GuardedStorage close) a few microtasks after harness.close,
		// before the released first page returns to the read. The Harness rejects its own calls earlier, as soon as the
		// Session is closing, while the guard still admits the next page until Close reaches the line. Close runs on another
		// goroutine here, so release the page only once a probe through the guarded Storage is refused.
		guarded := harness.(interface{ Storage() durable.Storage }).Storage()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
			if _, err := guarded.Conversation(testContext, root.Id()); errors.Is(err, session.ErrSessionClosed) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the guarded Storage still admits calls 10s after Close began")
			}
		}
		held.release()
		if err := <-reading; err == nil || !strings.Contains(err.Error(), "is closed") {
			t.Fatalf("the read returned %v, want the Session closed", err)
		}
		if err := <-closing; err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		pages := slices.Clone(store.pages)
		store.mu.Unlock()
		if !slices.Equal(pages, []string{"first", "close"}) {
			t.Fatalf("Storage calls were %v, want [first close]", pages)
		}
		sfEnded(t, harness, durable.SessionEndClosed, nil)
	})

	// harness-storage-failure.test.ts:192 it("fails it when Storage cannot close; an earlier failure stays the cause")
	t.Run("fails it when Storage cannot close; an earlier failure stays the cause", func(t *testing.T) {
		closeErr := errors.New("close failed")
		healthy := newFailingCalls()
		healthy.closeErr = closeErr
		harness, _, reports := sfStarted(t, healthy, &signalLog{})
		if err := harness.Close(testContext); !errors.Is(err, closeErr) {
			t.Fatalf("close returned %v, want the close error", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, closeErr)
		sfReports(t, reports, closeErr)

		store := newFailingCalls()
		store.closeErr = closeErr
		failedHarness, root, failedReports := sfStarted(t, store, &signalLog{})
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		if err := sfAppendNote(root); !errors.Is(err, failure) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		sfEnded(t, failedHarness, durable.SessionEndFailed, failure)
		sfReports(t, failedReports, failure)
	})

	// harness-storage-failure.test.ts:216 it("rejects the Harness's own calls with SessionFailed after a failure")
	t.Run("rejects the Harness's own calls with SessionFailed after a failure", func(t *testing.T) {
		store := newControlledStorage()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		if err := sfAppendNote(root); !errors.Is(err, failure) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		func() {
			defer func() {
				recovered, _ := recover().(error)
				sfSessionFailed(t, recovered, failure)
			}()
			harness.Resume()
		}()
		_, err := harness.Root(testContext, nil)
		sfSessionFailed(t, err, failure)
		_, err = harness.Conversation(testContext, root.Id())
		sfSessionFailed(t, err, failure)
		_, err = harness.CreateConversation(testContext, ConversationCreateOptions{Ownership: ownerless})
		sfSessionFailed(t, err, failure)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:267 it("fails it from a failed ID mint")
	t.Run("fails it from a failed ID mint", func(t *testing.T) {
		store := newFailingCalls()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		store.add("mintId")
		if err := sfAppendNote(root); !errors.Is(err, store.err) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, store.err)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:291 it("fails it for a StorageRequestError from a commit, whose effect is unknown")
	t.Run("fails it for a StorageRequestError from a commit, whose effect is unknown", func(t *testing.T) {
		store := newControlledStorage()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		rejection := durable.NewStorageRequestError("bad batch")
		store.failNextCommit(rejection)
		if err := sfAppendNote(root); !errors.Is(err, rejection) {
			t.Fatalf("the failed call returned %v, want the request error", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, rejection)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:232 it("ends what a task holds as failed, not as cancelled by its signal")
	t.Run("ends what a task holds as failed, not as cancelled by its signal", func(t *testing.T) {
		type ends struct {
			watch durable.WatchEnd
			idle  error
		}
		watching, finished := deferred(), make(chan ends, 1)
		type none = struct{}
		watcher := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
			Name:    "test.watcher",
			Version: 1,
			Initial: func(none) stepState { return stepState{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
				"work": func(ctx context.Context, _ durable.RunningTask[none, stepState, durable.JsonValue], runtime durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					doc, err := runtime.WatchDocErased(ctx, LiveDoc, runtime.ConversationId())
					if err != nil {
						return err
					}
					handle, err := runtime.Conversation(ctx, runtime.ConversationId())
					if err != nil {
						return err
					}
					idle := make(chan error, 1)
					go func() { idle <- handle.WaitForIdle(ctx) }()
					watching.resolve()
					<-doc.Closed()
					finished <- ends{doc.End(), <-idle}
					return nil
				},
			},
			Abort: func(context.Context, durable.RunningTask[none, stepState, durable.JsonValue], durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				return nil
			},
		})
		store := newControlledStorage()
		harness, _, _ := openTasks(t, store, []durable.AnyTask{watcher.Erased()})
		root := mustRoot(t, harness, nil)
		must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, watcher, none{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}))
		harness.Resume()
		_ = watching.wait(testContext)
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		if err := sfAppendNote(root); !errors.Is(err, failure) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, failure)
		got := <-finished
		if got.watch.Reason != durable.WatchFailed || !errors.Is(got.watch.Error, failure) {
			t.Fatalf("the task's watch ended %+v, want failed", got.watch)
		}
		sfSessionFailed(t, got.idle, failure)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:276 it("does not commit what a callback did after it caught a failed read")
	t.Run("does not commit what a callback did after it caught a failed read", func(t *testing.T) {
		store := newFailingCalls()
		harness, root, _ := sfStarted(t, store, &signalLog{})
		written := store.commitCount()
		store.failTask()
		_, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
			_, _ = tx.Task(1)
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		})
		sfSessionFailed(t, err, store.err)
		if got := store.commitCount(); got != written {
			t.Fatalf("%d commits admitted after the caught failure, want %d", got, written)
		}
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:301 it("fails only the call for an invalid request")
	t.Run("fails only the call for an invalid request", func(t *testing.T) {
		harness, root, reports := sfStarted(t, storage.NewMemoryStorage(), &signalLog{})
		entry := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.EntryRecord, error) {
			return tx.AppendEntry(root.Id(), durable.EntryDraft{Kind: "note"})
		}))
		ascending, descending := durable.ScanAscending, durable.ScanDescending
		requests := map[string]func() error{
			"a cursor from no scan": func() error {
				_, err := root.Entries(testContext, durable.EntryQuery{}, 10, durable.Cursor{"after": "x"})
				return err
			},
			"a cursor of the other order": func() error {
				page, err := root.Entries(testContext, durable.EntryQuery{Order: &ascending}, 1, nil)
				if err != nil {
					return err
				}
				cursor := durable.Cursor{"after": 1.0, "order": "ascending"}
				if page.Next != nil {
					cursor = *page.Next
				}
				_, err = root.Entries(testContext, durable.EntryQuery{Order: &descending}, 1, cursor)
				return err
			},
			"an unknown conversation": func() error {
				_, err := root.Commit(testContext, func(tx durable.Tx) (any, error) {
					return tx.ScanEntries(durable.EntryQuery{ConversationId: 999_999}, 10, nil)
				})
				return err
			},
			// pi.live keeps no history.
			"the history of a document that keeps none": func() error {
				_, err := harness.SnapshotAsOfErased(testContext, LiveDoc, entry.Id, root.Id())
				return err
			},
		}
		for _, name := range []string{"a cursor from no scan", "a cursor of the other order", "an unknown conversation", "the history of a document that keeps none"} {
			if err := requests[name](); !isStorageRequestError(err) {
				t.Fatalf("%s returned %v, want a StorageRequestError", name, err)
			}
		}
		if err := sfAppendNote(root); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
		select {
		case <-harness.Closed():
			t.Fatal("an invalid request ended the Session")
		default:
		}
		sfReports(t, reports)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:328 it("fails it for a throw in its own commit listeners, which would leave memory behind storage")
	t.Run("fails it for a throw in its own commit listeners, which would leave memory behind storage", func(t *testing.T) {
		harness, root, reports := sfStarted(t, newControlledStorage(), &signalLog{})
		bug := errors.New("listener bug")
		harness.(*harnessImpl).ObserveCommits(func(context.Context, durable.CommitPublication) { panic(bug) })
		// The commit itself is durable and returns; the Session fails behind it.
		if err := sfAppendNote(root); err != nil {
			t.Fatalf("the commit returned %v, want success", err)
		}
		sfEnded(t, harness, durable.SessionEndFailed, bug)
		sfReports(t, reports, bug)
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:342 it("fails it for a storage error during a host close, before the backend closes")
	t.Run("fails it for a storage error during a host close, before the backend closes", func(t *testing.T) {
		store := newControlledStorage()
		harness, root, reports := sfStarted(t, store, &signalLog{})
		held := store.holdCommits()
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		committing := make(chan error, 1)
		go func() { committing <- sfAppendNote(root) }()
		_ = held.entered.wait(testContext)
		closing := make(chan error, 1)
		go func() { closing <- harness.Close(testContext) }()
		flush()
		held.release()
		if err := <-committing; !errors.Is(err, failure) {
			t.Fatalf("the commit returned %v, want the storage error", err)
		}
		<-closing
		sfEnded(t, harness, durable.SessionEndFailed, failure)
		sfReports(t, reports, failure)
	})

	// harness-storage-failure.test.ts:410 it("does not fail it for a read its caller cancelled")
	t.Run("does not fail it for a read its caller cancelled", func(t *testing.T) {
		cancelled := errors.New("caller gave up")
		ctx, cancel := context.WithCancelCause(testContext)
		store := newFailingCalls()
		store.cancelCause = cancelled
		harness, root, _ := sfStarted(t, store, &signalLog{})
		if err := sfAppendNote(root); err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		store.cancelRead = func() { cancel(cancelled) }
		store.mu.Unlock()
		if _, err := root.Context(ctx, nil); !errors.Is(err, cancelled) {
			t.Fatalf("the cancelled read returned %v, want the caller's cause", err)
		}
		if err := sfAppendNote(root); err != nil {
			t.Fatalf("commit after a cancelled read returned %v, want success", err)
		}
		_ = harness.Close(testContext)
	})

	// harness-storage-failure.test.ts:358 it("ends an abort that joins a run ignoring its signal, and a reentrant report finds the Session failed")
	t.Run("ends an abort that joins a run ignoring its signal, and a reentrant report finds the Session failed", func(t *testing.T) {
		release, entered := deferred(), deferred()
		type none = struct{}
		stubborn := durable.DefineTask(durable.TaskDefinition[none, stepState, durable.JsonValue, any]{
			Name:    "test.stubborn",
			Version: 1,
			Initial: func(none) stepState { return stepState{Phase: "work"} },
			Phases: map[string]durable.PhaseHandler[none, stepState, durable.JsonValue, any]{
				"work": func(context.Context, durable.RunningTask[none, stepState, durable.JsonValue], durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
					entered.resolve()
					_ = release.wait(testContext)
					return nil
				},
			},
			Abort: func(context.Context, durable.RunningTask[none, stepState, durable.JsonValue], durable.TaskRuntime[none, stepState, durable.JsonValue, any]) error {
				return nil
			},
		})
		store := newControlledStorage()
		registry := CreateRegistry()
		mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{stubborn.Erased()}}))
		var harness Harness
		reentered := make(chan error, 1)
		var once sync.Once
		var opened sync.WaitGroup
		opened.Add(1)
		harness = must(OpenHarness(testContext, store, HarnessOptions{Models: ai.CreateModels(), Registry: registry, OnReport: func(error, durable.ReportInfo) {
			once.Do(func() {
				opened.Wait()
				// A report handler that calls back finds the Session already failed.
				reentered <- harness.WaitForIdle(testContext)
			})
		}}))
		opened.Done()
		root := mustRoot(t, harness, nil)
		id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
			return durable.CreateTask(tx, stubborn, none{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
		}))
		harness.Resume()
		_ = entered.wait(testContext)
		aborting := goCall(func() error { _, err := harness.AbortTask(testContext, id); return err })
		// The abort mark commits and the abort joins the run, which ignores its signal; then the Session fails.
		eventually(t, func() bool {
			record, _ := harness.GetTask(testContext, id)
			return record != nil && record.AbortRequested
		})
		time.Sleep(5 * time.Millisecond)
		failure := errors.New("disk gone")
		store.failNextCommit(failure)
		if err := sfAppendNote(root); !errors.Is(err, failure) {
			t.Fatalf("the failed call returned %v, want the storage error", err)
		}
		sfSessionFailed(t, aborting.wait(t), failure)
		select {
		case err := <-reentered:
			sfSessionFailed(t, err, failure)
		case <-time.After(10 * time.Second):
			t.Fatal("the report handler's call did not return")
		}
		release.resolve()
		_ = harness.Close(testContext)
	})
}

// harness-storage-failure.test.ts:603 (also TestSafeNow in harness_listener_errors_test.go).
// TestSafeNowReportsAPanickingClockOnceAlsoWhenTheReportHandlerReadsTheClockAgain ports "reports a throwing clock once, also
// when the report handler reads the clock again" (harness.ts safeNow).
// mutation-checked: reporting on every panic, or marking the clock reported only after the report returns, fails it.
func TestSafeNowReportsAPanickingClockOnceAlsoWhenTheReportHandlerReadsTheClockAgain(t *testing.T) {
	var reports []string
	var clock func() float64
	clock = SafeNow(func() float64 { panic(errors.New("clock broke")) }, func(err error, _ durable.ReportInfo) {
		reports = append(reports, err.Error())
		// Reentry, as a handler that commits does: the clock is read again before this report returns.
		if len(reports) < 5 {
			clock()
		}
	})
	if got := clock(); got < 1 {
		t.Fatalf("clock() = %v, want the system clock", got)
	}
	clock()
	if len(reports) != 1 || reports[0] != "clock broke" {
		t.Fatalf("reports %v, want [clock broke]", reports)
	}
}

// harness-storage-failure.test.ts:620 (also TestSafeNow in harness_listener_errors_test.go).
// TestAHarnessKeepsWorkingWhenOnReportPanicsAndFallsBackToTheSystemClockWhenTheClockPanics ports "keeps working when onReport
// throws, and falls back to Date.now when the clock throws" (harness.ts safeReport and safeNow).
// mutation-checked: handing the host's OnReport or Now to the Session unwrapped fails it.
func TestAHarnessKeepsWorkingWhenOnReportPanicsAndFallsBackToTheSystemClockWhenTheClockPanics(t *testing.T) {
	log := &signalLog{}
	registry := CreateRegistry()
	mustInstall(t, registry, new(durable.Extension{Name: "tasks", Tasks: []durable.AnyTask{waitingTask(log).Erased()}}))
	harness, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
		Models:   ai.CreateModels(),
		Registry: registry,
		Now:      func() float64 { panic(errors.New("clock broke")) },
		OnReport: func(error, durable.ReportInfo) { panic(errors.New("report broke")) },
	})
	if err != nil {
		t.Fatal(err)
	}
	root := mustRoot(t, harness, nil)
	id := must(durable.Commit(testContext, root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, waitingTask(log), struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
	}))
	harness.Resume()
	eventually(t, func() bool {
		record, _ := harness.GetTask(testContext, id)
		return record != nil && record.State.Status == durable.TaskRunning
	})
	record, err := harness.GetTask(testContext, id)
	if err != nil || record.StartedAt == nil || *record.StartedAt < 1 {
		t.Fatalf("task %+v (%v), want a startedAt from the system clock", record, err)
	}
	if _, err := harness.AbortTask(testContext, id); err != nil {
		t.Fatal(err)
	}
	settled, err := harness.WaitForTask(testContext, id)
	if err != nil || settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeAborted {
		t.Fatalf("settled %+v (%v), want aborted", settled, err)
	}
	if err := harness.Close(testContext); err != nil {
		t.Fatal(err)
	}
}
