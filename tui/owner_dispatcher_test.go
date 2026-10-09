package tui

import (
	"context"
	"errors"
	"io"
	"testing"
)

// PostToOwner queues work on the owner loop (SetOwnerDispatcher); without a dispatcher the work runs inline like the other
// dispatch seams, a nil fn is a no-op, and the dispatcher's error (ctx ended) is returned.
func TestPostToOwnerDelegatesToTheOwnerDispatcher(t *testing.T) {
	ui := NewWithOutput(io.Discard, 40, 10)
	ran := 0
	if err := ui.PostToOwner(t.Context(), func() { ran++ }); err != nil || ran != 1 {
		t.Fatalf("without a dispatcher: err=%v ran=%d, want inline run", err, ran)
	}
	var queued []func()
	ui.SetOwnerDispatcher(func(_ context.Context, fn func()) error { queued = append(queued, fn); return nil })
	if err := ui.PostToOwner(t.Context(), func() { ran++ }); err != nil || ran != 1 || len(queued) != 1 {
		t.Fatalf("with a dispatcher: err=%v ran=%d queued=%d, want the work queued, not run", err, ran, len(queued))
	}
	queued[0]()
	if ran != 2 {
		t.Fatalf("queued work ran %d times", ran)
	}
	if err := ui.PostToOwner(t.Context(), nil); err != nil || len(queued) != 1 {
		t.Fatalf("nil fn: err=%v queued=%d", err, len(queued))
	}
	boom := errors.New("owner loop gone")
	ui.SetOwnerDispatcher(func(context.Context, func()) error { return boom })
	if err := ui.PostToOwner(t.Context(), func() {}); !errors.Is(err, boom) {
		t.Fatalf("dispatcher error = %v, want %v", err, boom)
	}
}
