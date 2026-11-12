// Ports packages/durable/test/harness-submissions.test.ts (Pi d9d535b6c, #11963, #11820), the case "reports no unhandled rejection
// for a wait cancelled before its result is delivered" (Submission.wait on util.ts Waiters.add, which Scheduler.waitForTask shares).
//
// Pi's fix attaches a no-op handler to the waiter promise because the promise reaches the caller only after the line delivers the
// wait's job, and a rejection in between was reported as unhandled. Go has no unhandled rejection: a waiter is a closed channel
// and a value the caller reads when it calls Waiter.Wait. What carries over is the rest of the contract the test pins: a wait whose
// context ends (or whose Harness closes) after its waiter registered, or whose context had ended before, gives the caller the
// cancellation cause (the close error) once the line delivers the job, the waiter stops being registered, and no goroutine
// stays behind, whichever way the rejection and the delivery are ordered. Waiters.Add already satisfies it, so the cases are
// the port and Go needed no change. They run for Submission.Wait and for Harness.WaitForTask, which registers through the same
// Waiters.Add but on the Session line, so its waiter registers only once the line is free (see openTaskTarget).

package harness

// pi: packages/durable/src/harness/util.ts

import (
	"bytes"
	"context"
	"errors"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// slowPublications holds the publication of each commit stored while hold is set, until release (SlowPublications in the Pi
// test delays it by 20 ms; a gate gives the same order without a timer). The Session publishes the next commit only after
// the held one ran, and delivers a line job's outcome after the publications before it.
type slowPublications struct {
	*controlledStorage
	hold    atomic.Bool
	held    atomic.Int32
	gate    chan struct{}
	release func()
}

func newSlowPublications() *slowPublications {
	storage := &slowPublications{controlledStorage: newControlledStorage(), gate: make(chan struct{})}
	var once sync.Once
	storage.release = func() { once.Do(func() { close(storage.gate) }) }
	return storage
}

func (storage *slowPublications) Publishing(_ context.Context, publish func()) {
	if !storage.hold.Load() {
		publish()
		return
	}
	storage.held.Add(1)
	go func() {
		<-storage.gate
		publish()
	}()
}

// waitTarget is one wait of the Harness whose Waiters registration is under test, with a commit whose publication is held.
type waitTarget struct {
	harness Harness
	storage *slowPublications
	// root is the conversation the held write is submitted to.
	root Conversation
	// written receives the result of the write submission whose publication is held; nil until the publication is held.
	written chan error
	// wait is the wait under test.
	wait func(ctx context.Context) error
	// pending is the Done channel of each waiter registered for it.
	pending func() []<-chan struct{}
}

// held holds the publication of a write, unless it already is. A wait that registers on the Session line (Harness.WaitForTask reads
// the task on it) registers only once the line is free, so such a target holds the publication after its waiter registered.
func (target *waitTarget) held(t *testing.T) {
	t.Helper()
	if target.written == nil {
		target.holdWrite(t, target.root)
	}
}

func pendingDone[K comparable, T any](waiters *Waiters[K, T], key K) []<-chan struct{} {
	waiters.mu.Lock()
	defer waiters.mu.Unlock()
	var done []<-chan struct{}
	for _, waiter := range waiters.sets[key] {
		done = append(done, waiter.Done())
	}
	return done
}

func receive[T any](t *testing.T, results <-chan T) T {
	t.Helper()
	var got T
	waitFor(t, func() bool {
		select {
		case got = <-results:
			return true
		default:
			return false
		}
	})
	return got
}

// holdWrite submits a write whose publication is held, so the job of a wait submitted after it is delivered only after release.
func (target *waitTarget) holdWrite(t *testing.T, root Conversation) {
	t.Helper()
	target.storage.hold.Store(true)
	target.written = make(chan error, 1)
	go func() {
		_, err := root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: "note", Data: map[string]any{"text": "x"}}})
		target.written <- err
	}()
	waitFor(t, func() bool { return target.storage.held.Load() > 0 })
}

// registered waits for the one waiter of the wait under test and returns its Done channel.
func (target *waitTarget) registered(t *testing.T) <-chan struct{} {
	t.Helper()
	var done []<-chan struct{}
	waitFor(t, func() bool {
		done = target.pending()
		return len(done) == 1
	})
	target.held(t)
	return done[0]
}

// cleanup closes the Harness when the test ends, after releasing the held publication that its close would wait for.
func (target *waitTarget) cleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = target.harness.Close(testContext) })
	t.Cleanup(target.storage.release)
}

func openSubmissionTarget(t *testing.T) *waitTarget {
	t.Helper()
	storage := newSlowPublications()
	setup := chatSetup(t)
	answer := unanswered()
	setup.Faux.SetResponses([]ai.FauxResponseStep{answer.step})
	harness, root := openChat(t, storage, setup)
	submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("hi")}))
	target := &waitTarget{harness: harness, storage: storage}
	target.cleanup(t)
	target.wait = func(ctx context.Context) error {
		_, err := submission.Wait(ctx)
		return err
	}
	target.pending = func() []<-chan struct{} {
		return pendingDone(&harness.(*harnessImpl).submissions.waiters, submission.Id())
	}
	// The run's own commits come first: one of them published after the hold would take the place of the write's, and the wait
	// would queue behind a publication that nothing releases yet. Once the request is sent, the run has nothing left to commit.
	waitFor(t, func() bool { return closedChannel(answer.reached) })
	target.root = root
	target.holdWrite(t, root)
	return target
}

// openTaskTarget waits for a live task with Harness.WaitForTask, which reads the task on the Session line and registers its waiter
// there: the publication is held once the waiter registered (registered), not before, or the wait would queue behind it.
func openTaskTarget(t *testing.T) *waitTarget {
	t.Helper()
	storage := newSlowPublications()
	fixtures := newOwnFixtures()
	opened := ownOpenHarness(t, fixtures, storage)
	id := must(durable.Commit(testContext, opened.root, func(tx durable.Tx) (durable.TaskId, error) {
		return durable.CreateTask(tx, fixtures.hold, holdInput{Name: "held"}, durable.TaskOptions{Ownership: conversationOwned})
	}))
	target := &waitTarget{harness: opened.harness, storage: storage, root: opened.root}
	target.cleanup(t)
	target.wait = func(ctx context.Context) error {
		_, err := opened.harness.WaitForTask(ctx, id)
		return err
	}
	target.pending = func() []<-chan struct{} {
		return pendingDone(&opened.harness.(*harnessImpl).tasks.taskWaiters, id)
	}
	return target
}

// waiterGoroutines is how many goroutines Waiters.Add started that have not returned. Each waits for its waiter to settle, then
// drops its context's stop function: a waiter that never settles, or a settle nobody lets it see, leaves it blocked for good.
func waiterGoroutines() int {
	var stacks bytes.Buffer
	if err := pprof.Lookup("goroutine").WriteTo(&stacks, 2); err != nil {
		panic(err)
	}
	return strings.Count(stacks.String(), "durable/harness.(*Waiters[...]).Add in goroutine")
}

func TestAWaitEndedBeforeItsResultIsDelivered(t *testing.T) {
	targets := []struct {
		name string
		open func(*testing.T) *waitTarget
	}{{"Submission.Wait", openSubmissionTarget}, {"Harness.WaitForTask", openTaskTarget}}
	for _, kind := range targets {
		t.Run(kind.name, func(t *testing.T) {
			// harness-submissions.test.ts "reports no unhandled rejection for a wait cancelled before its result is delivered": the
			// context ends while the wait's job waits on the held publication of the commit before it.
			t.Run("gives the caller the cause of a context that ends after the waiter registered", func(t *testing.T) {
				target := kind.open(t)
				before := waiterGoroutines()
				cause := errors.New("stop waiting")
				ctx, abort := cancelledContext(cause)
				result := make(chan error, 1)
				go func() { result <- target.wait(ctx) }()
				done := target.registered(t)
				if waiterGoroutines() <= before {
					t.Fatal("no goroutine for the registered waiter: the leak check below would pass without looking")
				}
				abort()
				waitFor(t, func() bool { return closedChannel(done) })
				if pending := target.pending(); len(pending) != 0 {
					t.Fatalf("%d waiters still registered after their context ended", len(pending))
				}
				target.storage.release()
				if err := receive(t, result); !errors.Is(err, cause) {
					t.Fatalf("wait = %v, want the cause %q", err, cause)
				}
				if err := receive(t, target.written); err != nil {
					t.Fatalf("the write whose publication was held = %v", err)
				}
				waitFor(t, func() bool { return waiterGoroutines() <= before })
			})

			// #11963: the context was already ended when the waiter was registered.
			t.Run("gives the caller the cause of a context that ended before the wait", func(t *testing.T) {
				target := kind.open(t)
				target.held(t)
				before := waiterGoroutines()
				cause := errors.New("stop waiting")
				ctx, abort := cancelledContext(cause)
				abort()
				result := make(chan error, 1)
				go func() { result <- target.wait(ctx) }()
				target.storage.release()
				if err := receive(t, result); !errors.Is(err, cause) {
					t.Fatalf("wait = %v, want the cause %q", err, cause)
				}
				if err := receive(t, target.written); err != nil {
					t.Fatalf("the write whose publication was held = %v", err)
				}
				if pending := target.pending(); len(pending) != 0 {
					t.Fatalf("%d waiters registered for a wait that ended", len(pending))
				}
				waitFor(t, func() bool { return waiterGoroutines() <= before })
			})

			// #11820: the Harness closes, and the waiter has no abort signal to reject it.
			t.Run("gives the caller the close error of a Harness that closes after the waiter registered", func(t *testing.T) {
				target := kind.open(t)
				before := waiterGoroutines()
				result := make(chan error, 1)
				go func() { result <- target.wait(testContext) }()
				done := target.registered(t)
				closing := make(chan error, 1)
				go func() { closing <- target.harness.Close(testContext) }()
				// The close rejects the waiter as it begins, while its drain still waits on the held publication.
				waitFor(t, func() bool { return closedChannel(done) })
				target.storage.release()
				expectError(t, receive(t, result), "Harness is closed")
				if err := receive(t, closing); err != nil {
					t.Fatalf("close = %v", err)
				}
				<-target.written
				if pending := target.pending(); len(pending) != 0 {
					t.Fatalf("%d waiters registered after the close", len(pending))
				}
				waitFor(t, func() bool { return waiterGoroutines() <= before })
			})
		})
	}
}
