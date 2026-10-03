package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Chord's withAbortSignal adds an abort listener, so the derived context is aborted within signal's abort() call.
// newLinkedSignal's cancel must therefore leave every context linked before it returns already cancelled, including one
// whose link races the cancellation.
func TestLinkedSignalCancelsRacingLinksBeforeReturning(t *testing.T) {
	cause := errors.New("aborted")
	for range 20000 {
		signal, cancel := newLinkedSignal(context.Background())
		linked := make(chan context.Context, 1)
		var start sync.WaitGroup
		start.Add(1)
		go func() {
			start.Wait()
			child, _ := linkedChild(context.Background(), signal)
			linked <- child
		}()
		start.Done()
		cancel(cause)
		child := <-linked
		// The link returned after cancel began; give an asynchronous fallback no time to run.
		if child.Err() == nil {
			select {
			case <-child.Done():
			case <-time.After(time.Second):
				t.Fatal("a context linked while its signal was cancelled was never cancelled")
			}
		}
		if got := context.Cause(child); !errors.Is(got, cause) {
			t.Fatalf("linked cause %v, want %v", got, cause)
		}
	}
}

// The abort-mark commit cancels a run invocation from its commit listener on the Session line; a handle operation queued
// behind that commit must already see its bound context cancelled when it runs.
func TestLinkedChildIsCancelledBeforeCancelReturns(t *testing.T) {
	cause := errors.New("aborted")
	for range 1000 {
		signal, cancel := newLinkedSignal(context.Background())
		child, release := linkedChild(context.Background(), signal)
		cancel(cause)
		if err := child.Err(); err == nil {
			t.Fatal("a linked context was still live when its signal's cancel returned")
		}
		if got := context.Cause(child); !errors.Is(got, cause) {
			t.Fatalf("linked cause %v, want %v", got, cause)
		}
		release()
	}
}

// A context derived from a linked signal carries its link registry but has its own cancellation; a context linked to it
// must follow that cancellation, not only the root's.
func TestLinkedChildFollowsADerivedSignal(t *testing.T) {
	root, cancelRoot := newLinkedSignal(context.Background())
	defer cancelRoot(context.Canceled)
	derived, cancelDerived := context.WithCancelCause(root)
	child, release := linkedChild(context.Background(), derived)
	defer release()
	cause := errors.New("derived aborted")
	cancelDerived(cause)
	select {
	case <-child.Done():
	case <-time.After(time.Second):
		t.Fatal("a context linked to a derived signal ignored the derived signal's cancellation")
	}
	if got := context.Cause(child); !errors.Is(got, cause) {
		t.Fatalf("linked cause %v, want %v", got, cause)
	}
}

// Releasing a link removes it from the signal's registry, so finished operations do not accumulate on a long invocation.
func TestLinkedChildReleaseDropsTheLink(t *testing.T) {
	signal, cancel := newLinkedSignal(context.Background())
	defer cancel(context.Canceled)
	links := signal.Value(signalLinkKey{}).(*signalLinks)
	for range 10 {
		_, release := linkedChild(context.Background(), signal)
		release()
	}
	links.mu.Lock()
	defer links.mu.Unlock()
	if len(links.children) != 0 {
		t.Fatalf("%d released links remain registered", len(links.children))
	}
}
