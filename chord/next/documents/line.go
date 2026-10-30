package documents

import "sync"

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

// Acquire blocks until every earlier holder released and returns this holder's release; releasing twice is harmless.
func (line *Line) Acquire() (release func()) {
	done := make(chan struct{})
	line.mu.Lock()
	previous := line.tail
	line.tail = done
	line.waiting++
	line.mu.Unlock()
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
