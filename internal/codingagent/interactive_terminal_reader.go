package codingagent

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// terminalInputSink takes what an interactiveTerminalReader reads, on the reading goroutine: the input router in a run.
type terminalInputSink interface {
	// begin runs when a read session starts, before its first read.
	begin()
	process(data []byte)
	// deadline reports when flush must run if no input arrives first, the zero time for never.
	deadline() time.Time
	flush()
	// fail reports the read error that ended the input.
	fail(err error)
}

// interactiveTerminalReader owns stdin across external-editor handoffs and hands each read to its sink on the reading goroutine. Pause cancels the readiness wait and joins a session that is reading; a session still delivering to its sink, which may wait on the owner loop calling pause, finishes that delivery without reading again, and the next session reads only after it. A chunk read while pause cancelled the session is retained for the next session instead of dropped, so no read is lost and no second reader is left on the terminal.
type interactiveTerminalReader struct {
	ctx      context.Context
	file     *os.File
	terminal *tui.ProcessTerminal
	sink     terminalInputSink
	cancel   context.CancelFunc
	done     chan struct{}
	// pending is a read its session took after pause; the next session delivers it first.
	pending []byte

	mu sync.Mutex
	// delivering: the session is in its sink, not on the terminal.
	delivering bool
}

func newInteractiveTerminalReader(ctx context.Context, file *os.File, sink terminalInputSink) *interactiveTerminalReader {
	r := &interactiveTerminalReader{ctx: ctx, file: file, terminal: tui.NewProcessTerminal(file, os.Stdout), sink: sink}
	r.resume()
	return r
}

func (r *interactiveTerminalReader) pause() {
	if r.cancel == nil {
		return
	}
	r.mu.Lock()
	r.cancel()
	delivering := r.delivering
	r.mu.Unlock()
	r.cancel = nil
	if !delivering {
		<-r.done
	}
}

func (r *interactiveTerminalReader) resume() {
	if r.cancel != nil || r.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	r.cancel = cancel
	previous, done := r.done, make(chan struct{})
	r.done = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		if !r.deliver(ctx, r.sink.begin) {
			return
		}
		if data := r.pending; data != nil {
			if !r.deliver(ctx, func() { r.sink.process(data) }) {
				return
			}
			r.pending = nil
		}
		receive := func(data []byte) {
			if !r.deliver(ctx, func() { r.sink.process(data) }) {
				r.pending = data
			}
		}
		expired := func() { r.deliver(ctx, r.sink.flush) }
		if err := tui.ReadInputStreamUntil(ctx, r.file, r.sink.deadline, receive, expired); err != nil && ctx.Err() == nil {
			r.deliver(ctx, func() { r.sink.fail(err) })
		}
	}()
}

// stop ends a reader whose context is cancelled: it joins every session, including one that was delivering.
func (r *interactiveTerminalReader) stop() {
	r.pause()
	if r.done != nil {
		<-r.done
	}
}

// deliver runs fn in the sink unless pause cancelled the session first, and reports whether it ran. Pause and deliver
// decide under mu, so pause joins only a session that will not enter the sink.
//
// The sink readies the main loop on this goroutine's P, and the session's next wait is a poll system call that keeps
// that P: the main loop would wait until another thread steals it (about 30 µs per key on the Linux lab host). deliver yields
// once, so the main loop runs here before the session blocks.
func (r *interactiveTerminalReader) deliver(ctx context.Context, fn func()) bool {
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return false
	}
	r.delivering = true
	r.mu.Unlock()
	fn()
	r.mu.Lock()
	r.delivering = false
	r.mu.Unlock()
	runtime.Gosched()
	return true
}
