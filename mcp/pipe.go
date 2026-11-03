package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Ports packages/mcp/src/transports/pipe.ts.

// McpPipe is a running program's stdio, as pi-env's RemoteProcess provides it.
type McpPipe interface {
	// Write sends data to the program's stdin.
	Write(data []byte) error
	// CloseStdin closes the program's stdin.
	CloseStdin()
	// Close kills the program (and what it started). The pipe owns cleaning up what the program started.
	Close()
	// Exited receives once when the program has ended, whatever the reason: nil, or the error that ended it
	// (a rejected `exited` upstream). The channel may be closed instead of sent on. It must also deliver after Close.
	// The transport is its only reader.
	Exited() <-chan error
}

// McpPipeHandlers are what PipeTransportOptions.Open connects to the program's output. The transport copies what
// the handlers receive, so a caller may reuse the slices.
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
	// CloseTimeoutMs is how long the program may take to exit after its stdin closed before Close kills it. Default: 2000.
	CloseTimeoutMs int
}

// PipeTransport is MCP's stdio transport over a program that something else runs, for example on another machine
// through pi-env. The transport closes when the program ends; an exit error that Close did not cause is reported as
// an error first.
type PipeTransport struct {
	TransportEvents
	options PipeTransportOptions
	lines   *jsonRPCLineReader

	mu      sync.Mutex
	pipe    McpPipe
	started bool
	closed  bool
	ended   bool
	done    chan struct{} // closed once the program ended and the close event fired
	writeMu sync.Mutex
}

// NewPipeTransport returns a transport that has not started its program.
func NewPipeTransport(options PipeTransportOptions) *PipeTransport {
	t := &PipeTransport{options: options, done: make(chan struct{})}
	t.lines = newJSONRPCLineReader(jsonRPCLineReaderOptions{
		maxMessageBytes: options.MaxMessageBytes,
		maxStderrBytes:  options.MaxStderrBytes,
		onStderr:        options.OnStderr,
		onMessage:       t.EmitMessage,
		onError:         t.EmitError,
	})
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

	pipe, err := t.options.Open(McpPipeHandlers{OnStdout: t.lines.readStdout, OnStderr: t.lines.readStderr})
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
		err := <-pipe.Exited()
		t.exit(err)
	}()
	return nil
}

// Send writes one message as a line on the program's stdin.
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
	return pipe.Write(payload)
}

// Close shuts the program down per the spec: it closes stdin and lets the program exit, then kills it after
// CloseTimeoutMs. It returns once the program ended and the transport notified its close listeners.
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
	pipe.CloseStdin()
	timeout := time.Duration(t.options.CloseTimeoutMs) * time.Millisecond
	if t.options.CloseTimeoutMs == 0 {
		timeout = defaultCloseTimeoutMs * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.done:
		return nil
	case <-timer.C:
	}
	pipe.Close()
	<-t.done
	return nil
}

// exit is the program ending: an error unless Close caused it, an unfinished message, then close.
func (t *PipeTransport) exit(err error) {
	t.mu.Lock()
	t.ended = true
	closed := t.closed
	t.mu.Unlock()
	if err != nil && !closed {
		t.EmitError(err)
	}
	t.lines.end()
	t.EmitClose()
	close(t.done)
}
