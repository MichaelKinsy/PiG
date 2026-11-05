package session_test

import (
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
)

// DeferUntilPublished is called from any goroutine, not only from an observer on the publishing one: a Harness's wait for idleness
// calls it from its caller's goroutine, and a call made while a commit is published queues behind the listeners of that publication.
// The commit reads what is queued on its publication as it arms it, which the other goroutine's call appends to: that read is guarded
// (the race detector fails an unguarded one), and every function runs once, queued or at once.
func TestDeferUntilPublishedFromAnotherGoroutineWhilePublicationsRun(t *testing.T) {
	// No host listener is registered: what a publication queues is only what other goroutines defer.
	controlled := sessiontest.NewControlledStorage()
	kernel := session.NewSessionImpl(controlled, session.Hooks{}, nil, nil)
	var ran, returned atomic.Int64
	// deferred is the calling goroutine's own; the test reads it once that goroutine ended.
	var deferred int64
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// A publication runs what is queued on it until nothing is: with at most a few queued at a time it ends.
			if deferred-ran.Load() >= 8 {
				runtime.Gosched()
				continue
			}
			kernel.DeferUntilPublished(func() { ran.Add(1) })
			deferred++
			returned.Add(1)
		}
	}()
	func() {
		defer func() {
			close(stop)
			<-stopped
		}()
		for i := range 50 {
			// The commit's publication is armed and not yet run while the storage still works after the commit's adoption: the other
			// goroutine's calls land on it then, as they may at any time.
			seen := returned.Load()
			controlled.AfterAdopt(func() {
				for returned.Load() == seen {
					runtime.Gosched()
				}
			})
			commit(t, kernel, func(tx durable.Tx) error { return mustDoc(t, tx, watchStateDoc).Set("value", float64(i)) })
		}
	}()
	if got := ran.Load(); got != deferred {
		t.Fatalf("%d of the %d deferred functions ran", got, deferred)
	}
}
