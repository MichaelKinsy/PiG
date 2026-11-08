package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Ports packages/mcp/src/transports/pipe.ts (Pi c158fa2ee).

const defaultPipeCloseTimeoutMs = 2_000

// McpPipe is a running program's stdio, as `@earendil-works/pi-env`'s `RemoteProcess` provides it: PiG's env.RemoteProcess satisfies it as it is.
type McpPipe interface {
	// Write sends data (a string or []byte) to the program's stdin, in order.
	Write(data any)
	// CloseStdin ends the program's stdin.
	CloseStdin()
	// Close kills the program (and what it started). The pipe owns cleaning up what the program started.
	Close()
	// Exited blocks until the program has ended, whatever the reason, and returns how: its exit code, or the error that ended it (Pi's `exited` promise
	// rejecting). It may be called any number of times and from any goroutine, and it must also return after Close.
	Exited() (int, error)
}

// McpPipeHandlers are what [PipeTransportOptions.Open] connects to the program's output. The pipe calls each handler one call at a time, in
// the order the bytes arrived, and may reuse the slice after the call returns.
type McpPipeHandlers struct {
	OnStdout func(bytes []byte)
	OnStderr func(bytes []byte)
}

// PipeTransportOptions configure a [PipeTransport].
type PipeTransportOptions struct {
	// Open starts the program; OnStdout and OnStderr receive its output from then on.
	Open            func(handlers McpPipeHandlers) (McpPipe, error)
	MaxMessageBytes int
	MaxStderrBytes  int
	OnStderr        func(chunk string)
	// CloseTimeoutMs is how long the program may take to exit after its stdin is closed before Close kills it. Default 2000.
	CloseTimeoutMs int
}

// PipeTransport is MCP's stdio transport over a program that something else runs, for example on another machine through pi-env. The
// transport closes when the program ends; an exit failure that Close did not cause is reported as an error first.
type PipeTransport struct {
	TransportEvents
	options PipeTransportOptions
	lines   lineReader

	// readMu serializes the output handlers with the end of the program, so the framing state has one user at a time.
	readMu  sync.Mutex
	writeMu sync.Mutex

	mu      sync.Mutex
	pipe    McpPipe
	started bool
	closed  bool
	ended   bool
}

// NewPipeTransport returns a transport that has not opened its program.
func NewPipeTransport(options PipeTransportOptions) *PipeTransport {
	t := &PipeTransport{options: options}
	t.lines = lineReader{
		events:          &t.TransportEvents,
		maxMessageBytes: options.MaxMessageBytes,
		maxStderrBytes:  options.MaxStderrBytes,
		onStderr:        options.OnStderr,
	}
	return t
}

// Options returns the transport's options.
func (t *PipeTransport) Options() PipeTransportOptions { return t.options }

// Stderr is the last MaxStderrBytes (default 64 KiB) of the program's stderr.
func (t *PipeTransport) Stderr() string { return t.lines.stderrTail() }

// Start opens the program.
func (t *PipeTransport) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("MCP pipe transport already started")
	}
	if t.closed {
		t.mu.Unlock()
		return NewMcpConnectionClosedError("")
	}
	t.started = true
	t.mu.Unlock()
	pipe, err := t.options.Open(McpPipeHandlers{
		OnStdout: func(bytes []byte) {
			t.readMu.Lock()
			defer t.readMu.Unlock()
			t.lines.readStdout(bytes)
		},
		OnStderr: func(bytes []byte) {
			t.readMu.Lock()
			defer t.readMu.Unlock()
			t.lines.readStderr(bytes)
		},
	})
	if err != nil {
		return err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		// Close ran while the program was starting and found nothing to shut down.
		pipe.Close()
		return NewMcpConnectionClosedError("")
	}
	t.pipe = pipe
	t.mu.Unlock()
	go func() {
		_, failure := pipe.Exited()
		t.exit(failure)
	}()
	return nil
}

// Send writes one message as a line to the program's stdin. Like Pi's pipe send it ignores the send signal (ctx): a line is written at once.
func (t *PipeTransport) Send(ctx context.Context, message JSONRPCMessage) error {
	return t.SendOrdered(ctx, message, nil)
}

// SendOrdered is Send that calls placed, if not nil, once the write holds its place: a later Send writes after it.
func (t *PipeTransport) SendOrdered(_ context.Context, message JSONRPCMessage, placed func()) error {
	t.mu.Lock()
	pipe, ok := t.pipe, t.pipe != nil && !t.closed && !t.ended
	t.mu.Unlock()
	if !ok {
		return NewMcpConnectionClosedError("")
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if placed != nil {
		placed()
	}
	pipe.Write(payload)
	return nil
}

// Close closes the program's stdin and lets it exit, then kills it once CloseTimeoutMs has passed. It returns after the program exited.
func (t *PipeTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	pipe, ended := t.pipe, t.ended
	t.mu.Unlock()
	if pipe == nil {
		t.EmitClose()
		return nil
	}
	if ended {
		return nil
	}
	// Shutdown per the spec: close stdin and let the program exit, then kill it.
	pipe.CloseStdin()
	timeout := time.Duration(t.options.CloseTimeoutMs) * time.Millisecond
	if t.options.CloseTimeoutMs == 0 {
		timeout = defaultPipeCloseTimeoutMs * time.Millisecond
	}
	exited := make(chan struct{})
	go func() {
		_, _ = pipe.Exited()
		close(exited)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-exited:
		return nil
	case <-timer.C:
	}
	pipe.Close()
	// Pi awaits `exited` after the kill; Go bounds the wait by the same time, so a pipe that never reports the exit cannot hold Close forever.
	kill := time.NewTimer(timeout)
	defer kill.Stop()
	select {
	case <-exited:
		return nil
	case <-kill.C:
		return fmt.Errorf("MCP program did not exit within %v after it was killed", timeout)
	}
}

// exit is the program ended: an error unless Close caused it, then the unfinished message, then the close.
func (t *PipeTransport) exit(failure error) {
	t.mu.Lock()
	t.ended = true
	closed := t.closed
	t.mu.Unlock()
	if failure != nil && !closed {
		t.EmitError(failure)
	}
	t.readMu.Lock()
	t.lines.end()
	t.readMu.Unlock()
	t.EmitClose()
}
