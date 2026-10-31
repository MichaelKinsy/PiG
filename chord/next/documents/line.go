package documents

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
)

// Ports packages/chord/src/next/documents/line.ts
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
	done := make(chan struct{})
	line.mu.Lock()
	previous := line.tail
	line.tail = done
	line.waiting++
	line.mu.Unlock()
	parking.Park(ctx)
	<-previous
	line.mu.Lock()
	line.waiting--
	line.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
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
