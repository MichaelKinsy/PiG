package daemon

// Ports packages/env/daemon/src/output.rs

import (
	"bufio"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// bulkLimit is the unsent bulk bytes above which unwindowed command output waits.
const bulkLimit = 4 * 1024 * 1024

type bulkFrame struct {
	frame  *Frame
	unsent *atomic.Int64
}

// output is the single writer of stdout. Control frames (results, errors, pings, watch events) always go before bulk
// command output, so a slow link delays output but never a cancel's result or a ping. Bulk output is bounded: readers
// of commands without an output window wait while too much of it is unsent, which slows the command down like a full
// pipe would.
type output struct {
	mu        sync.Mutex
	control   []*Frame
	bulk      []bulkFrame
	bulkBytes int
	closed    bool
	// queued is signalled when a frame is queued or the writer stops.
	queued *sync.Cond
	// drained is closed and replaced when bulk bytes were written.
	drained chan struct{}
}

func newOutput() *output {
	out := &output{drained: make(chan struct{})}
	out.queued = sync.NewCond(&out.mu)
	return out
}

func (o *output) sendControl(frame *Frame) {
	o.mu.Lock()
	o.control = append(o.control, frame)
	o.mu.Unlock()
	o.queued.Signal()
}

// sendBulk queues bulk output; unsent counts this sender's frames until they are written.
func (o *output) sendBulk(frame *Frame, unsent *atomic.Int64) {
	o.mu.Lock()
	o.bulkBytes += len(frame.Payload)
	if unsent != nil {
		unsent.Add(1)
	}
	o.bulk = append(o.bulk, bulkFrame{frame, unsent})
	o.mu.Unlock()
	o.queued.Signal()
}

// waitForRoom waits while more than the bulk limit is unsent, unless stop is closed.
func (o *output) waitForRoom(stop <-chan struct{}) {
	var poll waitTimer
	defer poll.stop()
	for {
		o.mu.Lock()
		full := o.bulkBytes > bulkLimit && !o.closed
		drained := o.drained
		o.mu.Unlock()
		if !full {
			return
		}
		select {
		case <-stop:
			return
		case <-drained:
		case <-poll.arm(100 * time.Millisecond):
		}
	}
}

// gate is the backpressure of the readers of one command: like a full pipe, a reader that read a chunk waits for room before
// it reads the next. A reader counts as held while it hands a chunk over and waits, so the command can tell a reader the
// link holds up from a pipe that went silent (Gate in output.rs).
type gate struct {
	out  *output
	held atomic.Int32
}

func newGate(out *output) *gate { return &gate{out: out} }

// pass runs deliver for a chunk just read, then waits for room unless stop is closed; it returns what deliver returned.
func (g *gate) pass(stop <-chan struct{}, deliver func() bool) bool {
	g.held.Add(1)
	delivered := deliver()
	g.out.waitForRoom(stop)
	g.held.Add(-1)
	return delivered
}

// isHeld reports whether a reader is handing over a chunk or waiting for room: its silence is not the end of the output.
func (g *gate) isHeld() bool { return g.held.Load() > 0 }

func (o *output) close() {
	o.mu.Lock()
	o.closed = true
	close(o.drained)
	o.drained = make(chan struct{})
	o.mu.Unlock()
	o.queued.Broadcast()
}

// run writes frames to writer until closed or a write fails.
func (o *output) run(writer io.Writer) {
	buffered := bufio.NewWriterSize(writer, 1<<20)
	for {
		o.mu.Lock()
		var (
			frame  *Frame
			unsent *atomic.Int64
		)
		for frame == nil {
			switch {
			case len(o.control) > 0:
				frame = o.control[0]
				o.control = o.control[1:]
			case len(o.bulk) > 0:
				next := o.bulk[0]
				o.bulk = o.bulk[1:]
				o.bulkBytes -= len(next.frame.Payload)
				frame, unsent = next.frame, next.unsent
			case o.closed:
				o.mu.Unlock()
				return
			default:
				o.queued.Wait()
			}
		}
		o.mu.Unlock()
		// Each frame is flushed as Pi's writer flushes it (output.rs, frame.rs write_frame): a frame that arrives while the
		// pipe is full waits behind the one frame being written, not behind a buffer of queued bulk output.
		failed := WriteFrame(buffered, frame) != nil || buffered.Flush() != nil
		if unsent != nil {
			unsent.Add(-1)
		}
		o.mu.Lock()
		close(o.drained)
		o.drained = make(chan struct{})
		o.mu.Unlock()
		if failed {
			o.close()
			return
		}
	}
}
