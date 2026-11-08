package runtime_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// apps/p2/src/runtime/commits.ts has no unit test upstream (the plugins that use it, locations, mcp, powershell, search
// and subagents, test it through their workers). These cases pin its contract: a relevant commit wakes a waiter that
// subscribed before the scan, a commit seen since `seen` answers at once, an aborted wait rejects with the abort's cause,
// and Close detaches.

// fakeSession is a session that publishes to its observers when the test says.
type fakeSession struct {
	mu        sync.Mutex
	observers map[int]durable.CommitObserver
	next      int
}

type fakeObservation struct {
	session *fakeSession
	id      int
}

func (o fakeObservation) Detach() {
	o.session.mu.Lock()
	delete(o.session.observers, o.id)
	o.session.mu.Unlock()
}

func (s *fakeSession) ObserveCommits(observer durable.CommitObserver) durable.CommitObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.observers == nil {
		s.observers = map[int]durable.CommitObserver{}
	}
	s.next++
	s.observers[s.next] = observer
	return fakeObservation{session: s, id: s.next}
}

func (s *fakeSession) publish(seq durable.Seq) {
	s.mu.Lock()
	observers := make([]durable.CommitObserver, 0, len(s.observers))
	for _, observer := range s.observers {
		observers = append(observers, observer)
	}
	s.mu.Unlock()
	for _, observer := range observers {
		observer.Advance(context.Background(), durable.CommitPublication{Seq: seq})
	}
}

func (s *fakeSession) attached() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.observers)
}

func even(publication durable.CommitPublication) bool { return publication.Seq%2 == 0 }

func TestCommitSignalWakesAWaiterAfterARelevantCommit(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	session := &fakeSession{}
	signal := p2runtime.NewCommitSignal(session, even)
	t.Cleanup(signal.Close)
	seen := signal.Version()
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(context.Background(), seen) }()
	time.Sleep(20 * time.Millisecond)
	session.publish(1) // not accepted: the waiter stays
	select {
	case err := <-woke:
		t.Fatalf("woken by an irrelevant commit: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	session.publish(2)
	if err := <-woke; err != nil {
		t.Fatal(err)
	}
	if signal.Version() != seen+1 {
		t.Fatalf("version %d, want %d", signal.Version(), seen+1)
	}
}

func TestCommitSignalAnswersAtOnceForACommitSinceSeen(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	session := &fakeSession{}
	signal := p2runtime.NewCommitSignal(session, even)
	t.Cleanup(signal.Close)
	seen := signal.Version()
	session.publish(4)
	if err := signal.Next(context.Background(), seen); err != nil {
		t.Fatal(err)
	}
}

func TestCommitSignalWakesEveryWaiter(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	session := &fakeSession{}
	signal := p2runtime.NewCommitSignal(session, even)
	t.Cleanup(signal.Close)
	seen := signal.Version()
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = signal.Next(context.Background(), seen)
		}()
	}
	time.Sleep(20 * time.Millisecond)
	session.publish(2)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCommitSignalRejectsAnAbortedWaitWithItsCause(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	session := &fakeSession{}
	signal := p2runtime.NewCommitSignal(session, even)
	t.Cleanup(signal.Close)
	reason := errors.New("stopped")
	ctx, cancel := context.WithCancelCause(context.Background())
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(ctx, signal.Version()) }()
	time.Sleep(20 * time.Millisecond)
	cancel(reason)
	if err := <-woke; !errors.Is(err, reason) {
		t.Fatalf("got %v, want the abort's cause", err)
	}
	// Already aborted: rejects before looking at the version.
	if err := signal.Next(ctx, -1); !errors.Is(err, reason) {
		t.Fatalf("already aborted: got %v", err)
	}
	// The aborted waiter is gone: a later commit wakes a new one only.
	session.publish(2)
}

func TestCommitSignalCloseDetaches(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	session := &fakeSession{}
	signal := p2runtime.NewCommitSignal(session, even)
	if session.attached() != 1 {
		t.Fatalf("attached %d", session.attached())
	}
	signal.Close()
	if session.attached() != 0 {
		t.Fatalf("attached %d after Close", session.attached())
	}
	session.publish(2)
	if signal.Version() != 0 {
		t.Fatalf("a closed signal counted a commit: %d", signal.Version())
	}
}
