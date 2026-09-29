package codingagent

import (
	"context"
	"os"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactiveTerminalReader owns stdin across external-editor handoffs. Pause cancels and joins the readiness wait; it retains a chunk already read instead of dropping it or leaving a second reader on the terminal.
type interactiveTerminalReader struct {
	ctx      context.Context
	file     *os.File
	terminal *tui.ProcessTerminal
	data     chan []byte
	errors   chan error
	cancel   context.CancelFunc
	done     chan struct{}
	pending  []byte
}

func newInteractiveTerminalReader(ctx context.Context, file *os.File) *interactiveTerminalReader {
	r := &interactiveTerminalReader{ctx: ctx, file: file, terminal: tui.NewProcessTerminal(file, os.Stdout), data: make(chan []byte), errors: make(chan error, 1)}
	r.resume()
	return r
}

func (r *interactiveTerminalReader) pause() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel = nil
}

func (r *interactiveTerminalReader) resume() {
	if r.cancel != nil || r.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		deliver := func(data []byte) {
			select {
			case r.data <- data:
			case <-ctx.Done():
				r.pending = data
			}
		}
		if r.pending != nil {
			data := r.pending
			r.pending = nil
			deliver(data)
		}
		if err := tui.ReadInputStream(ctx, r.file, deliver); err != nil && ctx.Err() == nil {
			r.errors <- err
		}
	}()
}
