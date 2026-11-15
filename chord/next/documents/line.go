package documents

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
)

// Ports packages/chord/src/documents/line.ts
//
// An object's line (§9): its transactions, cold loads, key pages and eviction sweeps run on it one at a time, in the
// order they asked. Go form of Pi's promise chain: a call takes its place under mu at the call (so the order is the
// order of the calls that reach Acquire) and waits for the holder before it, as the store's commit queue does.

// Line is an object's line.
type Line struct {
	mu   sync.Mutex
	tail chan struct{}
	// waiting counts the holders that took their place and wait for the one before them.
	waiting int
}

// NewLine is a free line.
func NewLine() *Line {
	free := make(chan struct{})
	close(free)
	return &Line{tail: free}
}

// Acquire blocks until every earlier holder released and returns this holder's release; releasing twice is harmless. A
// handler's ctx parks the handler once its place is taken (Pi's `await line.acquire()` takes the place in the section that
// calls it and resumes after it): ctx is the caller's; context.Background() for a caller that is no handler.
func (line *Line) Acquire(ctx context.Context) (release func()) {
	return line.reserve().wait(ctx)
}

// linePlace is a holder's place on the line, taken and not yet waited for.
type linePlace struct {
	line     *Line
	previous <-chan struct{}
	done     chan struct{}
}

// reserve takes this holder's place at the call, as Pi's `line.acquire()` does when it is called: the order of the holders
// is the order of the calls that reach reserve. wait is the rest of Acquire.
func (line *Line) reserve() linePlace {
	done := make(chan struct{})
	line.mu.Lock()
	previous := line.tail
	line.tail = done
	line.waiting++
	line.mu.Unlock()
	return linePlace{line: line, previous: previous, done: done}
}

// wait parks a handler's ctx, blocks until every earlier holder released and returns this holder's release. A task of the host's
// loop (awaitTask: `await documents.settled()` of the teardown, a cold load) that has to wait for the holder before it waits for
// something the loop does not run (a commit a test holds, a step still writing): it is parked, which a test's join does not
// wait for (Pi's `setTimeout(0)` does not wait for a promise still pending on the line either); the wait is read without
// being consumed, since a closed channel stays closed.
func (place linePlace) wait(ctx context.Context) (release func()) {
	parking.Park(ctx)
	select {
	case <-place.previous:
	default:
		resume := parking.Idle(func() bool {
			select {
			case <-place.previous:
				return true
			default:
				return false
			}
		})
		<-place.previous
		resume()
	}
	place.line.mu.Lock()
	place.line.waiting--
	place.line.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(place.done) }) }
}

// waitingHolders is the number of holders that asked and have not been handed the line yet.
func (line *Line) waitingHolders() int {
	line.mu.Lock()
	defer line.mu.Unlock()
	return line.waiting
}

// WaitingHolders is the number of holders that asked for the line and have not been handed it yet: what a test of the
// host reads to know that a transaction took its place (Pi's tests rely on the order of promise calls).
func (line *Line) WaitingHolders() int { return line.waitingHolders() }
