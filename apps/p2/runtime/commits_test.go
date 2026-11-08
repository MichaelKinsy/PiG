package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// A CommitSignal wakes its waiters after the commits its filter accepts, and nothing else (commits.ts).

type fakeSource struct {
	observer durable.CommitObserver
	detached bool
}

func (f *fakeSource) ObserveCommits(observer durable.CommitObserver) durable.CommitObservation {
	f.observer = observer
	return detach(func() { f.detached = true })
}

type detach func()

func (d detach) Detach() { d() }

func TestCommitSignalWakesOnAcceptedCommitsOnly(t *testing.T) {
	source := &fakeSource{}
	signal := NewCommitSignal(source, func(publication durable.CommitPublication) bool { return publication.Seq == 7 })
	seen := signal.Version()
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(context.Background(), seen) }()
	// A commit the filter refuses wakes nobody.
	source.observer.Advance(context.Background(), durable.CommitPublication{Seq: 1})
	select {
	case err := <-woke:
		t.Fatalf("woken by a refused commit: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if signal.Version() != seen {
		t.Fatalf("version %d", signal.Version())
	}
	source.observer.Advance(context.Background(), durable.CommitPublication{Seq: 7})
	select {
	case err := <-woke:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an accepted commit must wake the waiter")
	}
	if signal.Version() != seen+1 {
		t.Fatalf("version %d", signal.Version())
	}
	// A version the waiter has not seen resolves at once.
	if err := signal.Next(context.Background(), seen); err != nil {
		t.Fatal(err)
	}
}

func TestCommitSignalNextEndsWithItsContext(t *testing.T) {
	source := &fakeSource{}
	signal := NewCommitSignal(source, func(durable.CommitPublication) bool { return true })
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("done")
	woke := make(chan error, 1)
	go func() { woke <- signal.Next(ctx, signal.Version()) }()
	time.Sleep(20 * time.Millisecond)
	cancel(cause)
	if err := <-woke; !errors.Is(err, cause) {
		t.Fatalf("error %v", err)
	}
	if err := signal.Next(ctx, signal.Version()); !errors.Is(err, cause) {
		t.Fatalf("an ended context refuses at once: %v", err)
	}
	signal.Close()
	if !source.detached {
		t.Fatal("Close must detach the observation")
	}
}
