package tui

// Ports packages/tui/src/terminal.ts.

import (
	"context"
	"io"
	"os"
)

// ReadInputStream delivers raw reads synchronously until EOF, a read error, or cancellation. The callback must return when ctx is cancelled. Files stay open for the next terminal owner. Other ReadClosers are closed on cancellation to interrupt Read; non-closable readers must complete each Read without waiting for external input.
func ReadInputStream(ctx context.Context, source io.Reader, onInput func([]byte)) error {
	reader, err := newTerminalReader(ctx, source)
	if err != nil {
		return err
	}
	defer reader.close()
	for {
		data, err := reader.read(-1)
		if len(data) != 0 {
			onInput(data)
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
	return ReadInputChunk(r.source)
}

func (r *terminalReader) close() {
	if r.waiter != nil {
		r.waiter.close()
	}
	if r.stopClose != nil && !r.stopClose() {
		<-r.closeDone
	}
}
