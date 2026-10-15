package tui

// Ports packages/tui/src/terminal.ts.

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// ReadInputStream delivers raw reads synchronously until EOF, a read error, or cancellation. The callback must return when ctx is cancelled. Files stay open for the next terminal owner. Other ReadClosers are closed on cancellation to interrupt Read; non-closable readers must complete each Read without waiting for external input.
func ReadInputStream(ctx context.Context, source io.Reader, onInput func([]byte)) error {
	return ReadInputStreamUntil(ctx, source, nil, onInput, nil)
}

// ReadInputStreamUntil is ReadInputStream for a reader that also owns input deadlines, such as TerminalInput's NextDeadline: while next reports a deadline, a terminal-file wait ends there and calls expired instead of waiting for input, as the framing timers' setTimeout fires on Pi's event loop between stdin reads. A nil next never ends a wait; other sources ignore deadlines.
func ReadInputStreamUntil(ctx context.Context, source io.Reader, next func() time.Time, onInput func([]byte), expired func()) error {
	reader, err := newTerminalReader(ctx, source)
	if err != nil {
		return err
	}
	defer reader.close()
	for {
		wait := -1
		if next != nil {
			if deadline := next(); !deadline.IsZero() {
				wait = int((max(time.Until(deadline), 0) + time.Millisecond - 1).Milliseconds())
			}
		}
		data, err := reader.read(wait)
		if len(data) != 0 {
			onInput(data)
		} else if err == nil && wait >= 0 {
			expired()
		}
		if err != nil {
			return err
		}
	}
}

type terminalReader struct {
	ctx       context.Context
	source    io.Reader
	file      *os.File
	waiter    *terminalInputWaiter
	stopClose func() bool
	closeDone chan struct{}
}

func newTerminalReader(ctx context.Context, source io.Reader) (*terminalReader, error) {
	r := &terminalReader{ctx: ctx, source: source}
	if file, ok := source.(*os.File); ok {
		r.file = file
		waiter, err := newTerminalInputWaiter(ctx)
		if err != nil {
			return nil, err
		}
		r.waiter = waiter
	} else if closer, ok := source.(io.ReadCloser); ok {
		r.closeDone = make(chan struct{})
		r.stopClose = context.AfterFunc(ctx, func() {
			_ = closer.Close()
			close(r.closeDone)
		})
	}
	return r, nil
}

// read returns no data on a deadline or a non-character console record. Bytes already read are returned even if cancellation raced the read.
func (r *terminalReader) read(ms int) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if r.file != nil && !terminalInputBuffered(r.file) {
		ready, err := r.waiter.wait(r.file, ms)
		if err != nil {
			return nil, err
		}
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		if !ready {
			return nil, nil
		}
		pending, err := terminalInputPending(r.file)
		if err != nil || !pending {
			return nil, err
		}
	}
	data, err := ReadInputChunk(r.source)
	if r.file != nil && errors.Is(err, syscall.EIO) && terminalHungUp(r.file) {
		// Closing a pseudo-terminal's master hangs the terminal up, and a read racing that hang-up reports either the end of input or EIO. Node reports the end of input every time, so a hung-up terminal is the end of input here too; an EIO from a terminal that is not hung up (an orphaned process group) stays an error.
		err = io.EOF
	}
	return data, err
}

func (r *terminalReader) close() {
	if r.waiter != nil {
		r.waiter.close()
	}
	if r.stopClose != nil && !r.stopClose() {
		<-r.closeDone
	}
}
