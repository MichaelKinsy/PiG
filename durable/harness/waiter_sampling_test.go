package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// The rule, once: a wait listens with chordctx.OnAbort, which runs inside the cancel that aborts its context, so for a chord
// context nothing has to sample ctx.Err() (the wait is rejected and out of the set when the cancel returns). A context that
// OnAbort cannot listen to entirely (its abort can come from outside chord/context) also gets context.AfterFunc, and Settle,
// RejectAll and Keys sample ctx.Err() for it, since its callback may not have run.
func TestAWaitSamplesItsContextOnlyWhenOnAbortCannotListenToIt(t *testing.T) {
	cases := []struct {
		name    string
		context func(t *testing.T) context.Context
		sampled bool
	}{
		{"a chord context", func(t *testing.T) context.Context {
			ctx, cancel := chordctx.WithCancel(chordctx.Background())
			t.Cleanup(func() { cancel(nil) })
			return ctx
		}, false},
		{"a chord context derived from a chord signal", func(t *testing.T) context.Context {
			signal, cancelSignal := chordctx.WithCancel(chordctx.Background())
			parent, cancelParent := chordctx.WithCancel(chordctx.Background())
			t.Cleanup(func() { cancelSignal(nil); cancelParent(nil) })
			return chordctx.WithAbortSignal(parent, signal)
		}, false},
		{"a context that cannot be cancelled", func(*testing.T) context.Context { return context.Background() }, false},
		{"a stdlib cancel context", func(t *testing.T) context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			return ctx
		}, true},
		{"a chord context under a stdlib cancel context", func(t *testing.T) context.Context {
			parent, cancelParent := context.WithCancel(context.Background())
			ctx, cancel := chordctx.WithCancel(parent)
			t.Cleanup(func() { cancel(nil); cancelParent() })
			return ctx
		}, true},
		{"a chord context made from a stdlib cancel context as its signal", func(t *testing.T) context.Context {
			signal, cancelSignal := context.WithCancel(context.Background())
			parent, cancelParent := chordctx.WithCancel(chordctx.Background())
			t.Cleanup(func() { cancelSignal(); cancelParent(nil) })
			return chordctx.WithAbortSignal(parent, signal)
		}, true},
		{"a context with a deadline", func(t *testing.T) context.Context {
			ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
			t.Cleanup(cancel)
			return ctx
		}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var waiters Waiters[int, string]
			waiter := waiters.Add(test.context(t), nil, 1)
			if waiter.sampled != test.sampled {
				t.Fatalf("sampled = %v, want %v", waiter.sampled, test.sampled)
			}
			waiters.RejectAll(errors.New("done with it"))
		})
	}
}
