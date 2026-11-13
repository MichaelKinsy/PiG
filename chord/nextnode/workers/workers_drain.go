package workers

import (
	"sync"
	"sync/atomic"
)

// Node tells a child's `exit` when the process ended, after what the child wrote before: the reads of its streams and the
// signal of its end are in one poll of the event loop, and libuv runs the signal's part last. It waits for no stream's end
// (that is `close`), so a process the child started that holds one of its streams holds neither its `exit` nor what it
// writes later, which the stream's listeners still get. It ports no Pi file: process.ts has that order from Node.
//
// drain is that rule for one stream a worker process writes (an output pipe, its channel) and the one goroutine that reads
// and tells it. At the process's end the waiter calls processEnded and waits for settled, which is closed once a read the
// goroutine began AFTER the end found nothing to read, or once the goroutine returned (the stream's end, this side's
// close). What the process wrote is in the stream by then: the kernel closes a process's descriptors, its writes done,
// before its parent's wait returns, and what was written stays readable. The goroutine reads again only after it told what
// its last read gave it, so nothing it read is untold when it settles. It then reads on, to the stream's end.
//
// No duration decides it. A reader that waits for the stream when the process ends is woken to look again (processEnded).
//
// Windows has no read of these streams that does not wait (workers_drain_windows.go): a drain there settles when its
// goroutine returns, so a worker's exit is told there when its streams ended, which the end of its job object brings.
type drain struct {
	// ended: the process ended. A reader loads it before the read whose finding nothing settles.
	ended   atomic.Bool
	settled chan struct{}
	once    sync.Once

	mu sync.Mutex
	// wake has a read that waits for the stream return to its reader; nil while nothing is read through reader's result.
	wake func()
}

func newDrain() *drain { return &drain{settled: make(chan struct{})} }

// settle closes settled, once.
func (d *drain) settle() { d.once.Do(func() { close(d.settled) }) }

// processEnded: the process that writes the stream ended. A reader that waits for the stream is woken to look again.
func (d *drain) processEnded() {
	d.ended.Store(true)
	d.mu.Lock()
	wake := d.wake
	d.mu.Unlock()
	if wake != nil {
		wake()
	}
}
