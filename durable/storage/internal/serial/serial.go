// Package serial is the one operation queue of the SQLite database adapters: operations run in call order and a
// transaction holds the queue until it settles (Pi's SerialQueue and the promise chain of NodeSqliteDatabase).
package serial

import (
	"errors"
	"sync"
)

// ErrTransactionInactive is returned by a transaction handle used after its callback returned.
var ErrTransactionInactive = errors.New("SQLite transaction handle is no longer active")

// Queue runs operations in call order. An operation starts immediately when nothing is running or waiting; otherwise
// it waits for everything before it.
type Queue struct {
	mu      sync.Mutex
	tail    chan struct{}
	waiting int
}

// NewQueue returns an idle queue.
func NewQueue() *Queue {
	ready := make(chan struct{})
	close(ready)
	return &Queue{tail: ready}
}

// Acquire takes the next turn and blocks until every earlier turn has been released.
func (queue *Queue) Acquire() (release func()) {
	queue.mu.Lock()
	previous := queue.tail
	turn := make(chan struct{})
	queue.tail = turn
	queue.waiting++
	queue.mu.Unlock()
	<-previous
	queue.mu.Lock()
	queue.waiting--
	queue.mu.Unlock()
	return func() { close(turn) }
}

// Queued reports how many turns wait behind the running one.
func (queue *Queue) Queued() int {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	return queue.waiting
}
