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
// Measured on Node v24.22.2 with process.ts's fork options and listeners (100 runs per size, the child writing its stdout
// synchronously and then exiting): all that was written before the exit was told before `exit` in 100 of 100 runs up to 64
// KiB, a pipe's capacity, and in 99, 97, 97, 84 and 64 of 100 at 65 KiB, 128 KiB, 256 KiB, 1 MiB and 4 MiB, where Node's
// other outcome is a tail told after `exit`. Telling all that was present at the exit before it is therefore one of the
// orders Node gives, never one it does not (the script is ~/lanes/Pr508Workers/tmp/lane/order.mjs on xd6702).
//
// drain is that rule for one stream a worker process writes (an output pipe, its channel) and the one goroutine that reads
// and tells it. At the process's end the waiter calls processEnded and waits for settled, which is closed once the
// goroutine told what the stream held at that end, or once the goroutine returned (the stream's end, this side's close).
// What the process wrote is in the stream by the end: the kernel closes a process's descriptors, its writes done, before
// its parent's wait returns, and what was written stays readable. So processEnded counts the bytes the stream holds then
// and the drain settles at the first read the goroutine begins after it told that many (the bytes it read since the count
// are taken off it, under the lock the count was made with: none is counted twice or missed). The count is of what is in the
// stream at the end, so it includes what a process outside the worker's tree wrote before the count too: that is present at
// the end, and told before the exit as well. The goroutine reads again only after it told what its last read gave it, so
// nothing it read is untold when it settles. It then reads on, to the stream's end.
//
// No duration decides it, and neither does the stream running empty: a process outside the tree that keeps writing the
// stream (its output, its channel) never lets a read find it empty, and the exit does not wait for what it writes after
// the count. A stream that cannot count what it holds (a platform without the request, workers_drain_count_other.go) is
// settled by a read that finds it empty instead. A reader that waits for the stream when the process ends is woken to look
// again (processEnded).
//
// Windows has no read of these streams that does not wait (workers_drain_windows.go): a drain there settles when its
// goroutine returns, so a worker's exit is told there when its streams ended, which the end of its job object brings.
type drain struct {
	settled chan struct{}
	once    sync.Once
	// returned is closed once the goroutine that reads and tells the stream returned: nothing of this stream is told after
	// it, which release waits for (WorkerProcess.awaitReaders).
	returned     chan struct{}
	returnedOnce sync.Once

	mu sync.Mutex
	// ended: the process ended. A reader begins its reads, and settles, under mu, which processEnded sets it under.
	ended atomic.Bool
	// owed is what the stream held at the end and the goroutine read none of yet; -1 when the stream could not count.
	owed int
	// holds counts the bytes the stream holds now, -1 when it cannot; nil for a stream the drain does not look at.
	holds func() int
	// wake has a read that waits for the stream return to its reader; nil while nothing is read through reader's result.
	wake func()
}

func newDrain() *drain {
	return &drain{settled: make(chan struct{}), returned: make(chan struct{}), owed: -1}
}

// settle closes settled, once.
func (d *drain) settle() { d.once.Do(func() { close(d.settled) }) }

// finish is the end of the goroutine that reads the stream: it has told all it read, so it settles, and it has returned.
func (d *drain) finish() {
	d.settle()
	d.returnedOnce.Do(func() { close(d.returned) })
}

// processEnded: the process that writes the stream ended. What the stream holds is counted, and a reader that waits for the
// stream is woken to look again.
func (d *drain) processEnded() {
	d.mu.Lock()
	d.ended.Store(true)
	if d.holds != nil {
		d.owed = d.holds()
	}
	wake := d.wake
	d.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// took takes what a read of count bytes read off what is owed. Call it holding mu, in the section of the read.
func (d *drain) took(count int) {
	if d.ended.Load() && d.owed > 0 {
		d.owed = max(0, d.owed-count)
	}
}
