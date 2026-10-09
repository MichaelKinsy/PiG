package client

import (
	"context"
	"sync"
)

// Ports packages/client/src/connection.ts (#openTransport awaits transportFactory and the transport's send).

// adaptFactory runs Pi's blocking ByteTransportFactory as the callback form Connection starts. The factory runs on one goroutine that the
// attempt owns: it ends when the factory returns, and Connection joins it through the completion. A handler call the factory makes is live,
// as it is in Pi; a transport that reads from a socket starts reading when the Connection has taken it (see readStarter).
func adaptFactory(factory ByteTransportFactory) callbackByteTransportFactory {
	return func(ctx context.Context, handlers ByteTransportHandlers, complete func(callbackByteTransport, error)) {
		go func() {
			transport, err := callByteTransportFactory(ctx, factory, handlers)
			if err != nil || transport == nil {
				complete(nil, err)
				return
			}
			complete(callbackTransport(transport), nil)
			if starter, ok := transport.(readStarter); ok {
				starter.startReading()
			}
		}()
	}
}

// readStarter is a transport whose socket events follow the continuation that awaits its factory, as Node's do: the Connection starts it once
// it owns the transport and has admitted the client hello, so no data reaches a Connection that is still connecting.
type readStarter interface{ startReading() }

// callbackTransport is the transport as the callback form: one that admits sends itself (Submit) is used as it is, so a frame is admitted
// before Connection.Send returns, as Pi's connection.ts:102-118 calls transport.send synchronously; any other ByteTransport is driven in order
// by sequentialTransport.
func callbackTransport(transport ByteTransport) callbackByteTransport {
	if direct, ok := transport.(callbackByteTransport); ok {
		return direct
	}
	return newSequentialTransport(transport)
}

// callByteTransportFactory is `await factory(handlers)`: a throw and a rejection are the same error.
func callByteTransportFactory(ctx context.Context, factory ByteTransportFactory, handlers ByteTransportHandlers) (transport ByteTransport, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			transport, err = nil, panicError(failure)
		}
	}()
	return factory(ctx, handlers)
}

type sequentialSend struct {
	chunk    []byte
	complete func(error)
}

// sequentialTransport is a ByteTransport as the callback form: Submit admits a chunk in order and one goroutine, alive only while chunks are
// queued, calls Send for each in turn, so the writes keep their invocation order and each completes once.
type sequentialTransport struct {
	inner  ByteTransport
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	queue   []sequentialSend
	running bool
}

func newSequentialTransport(inner ByteTransport) *sequentialTransport {
	ctx, cancel := context.WithCancel(context.Background())
	return &sequentialTransport{inner: inner, ctx: ctx, cancel: cancel}
}

func (t *sequentialTransport) Submit(chunk []byte, complete func(error)) {
	t.mu.Lock()
	t.queue = append(t.queue, sequentialSend{chunk: chunk, complete: complete})
	start := !t.running
	t.running = true
	t.mu.Unlock()
	if start {
		go t.drain()
	}
}

func (t *sequentialTransport) drain() {
	for {
		t.mu.Lock()
		if len(t.queue) == 0 {
			t.running = false
			t.mu.Unlock()
			return
		}
		next := t.queue[0]
		t.queue = t.queue[1:]
		t.mu.Unlock()
		next.complete(t.send(next.chunk))
	}
}

// send is `await transport.send(chunk)`: a throw and a rejection are the same error.
func (t *sequentialTransport) send(chunk []byte) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = panicError(failure)
		}
	}()
	return t.inner.Send(t.ctx, chunk)
}

// Close closes the transport, then cancels the waits of the writes it still owes.
func (t *sequentialTransport) Close() {
	t.inner.Close()
	t.cancel()
}

// Done is the wrapped transport's own lifetime, when it has one.
func (t *sequentialTransport) Done() <-chan struct{} {
	if lifetime, ok := t.inner.(interface{ Done() <-chan struct{} }); ok {
		return lifetime.Done()
	}
	return nil
}
