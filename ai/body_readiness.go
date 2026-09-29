package ai

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
)

// bodyReadiness is the transport side of the readiness bridge. It reports whether a body read completes in the current turn or becomes an external completion, using only what the transport knows: bytes its own buffer already holds are ready; a read that must wait for the network is pending. No sleep, goroutine schedule, or channel length decides it.
type bodyReadiness interface {
	// begin starts the only outstanding read. When the transport already holds the result, begin returns it with ready true. Otherwise begin returns ready false and calls deliver exactly once, from any goroutine, when the read finishes.
	begin(deliver func(bodyReadSignal)) (result bodyReadSignal, ready bool)
	// Close abandons the body and joins every goroutine the readiness source owns.
	Close() error
}

// observedBodyReadiness reads an HTTP/1 response body on an owned goroutine. The connection announces a network wait through bodyReadOwner before it blocks; a read that returns without the announcement was served from the transport's buffer.
type observedBodyReadiness struct {
	body     *observedResponseBody
	requests chan *bodyReadOperation
	joined   chan struct{}
	close    sync.Once
	err      error
}

func newObservedBodyReadiness(body *observedResponseBody) *observedBodyReadiness {
	readiness := &observedBodyReadiness{body: body, requests: make(chan *bodyReadOperation), joined: make(chan struct{})}
	go readiness.readBody()
	return readiness
}

func (readiness *observedBodyReadiness) readBody() {
	defer close(readiness.joined)
	for operation := range readiness.requests {
		buffer := make([]byte, bufio.MaxScanTokenSize)
		readiness.body.begin(operation)
		count, err := readiness.body.Read(buffer)
		readiness.body.clear(operation)
		operation.complete(buffer[:count], err)
	}
}

func (readiness *observedBodyReadiness) begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	operation := &bodyReadOperation{signals: make(chan bodyReadSignal, 1), onPendingComplete: deliver}
	readiness.requests <- operation
	signal := <-operation.signals
	if signal.pending {
		return bodyReadSignal{}, false
	}
	return signal, true
}

func (readiness *observedBodyReadiness) Close() error {
	readiness.close.Do(func() {
		readiness.err = readiness.body.Close()
		close(readiness.requests)
		<-readiness.joined
	})
	return readiness.err
}

// opaqueBodyReadiness reads a body that cannot report its buffer state, such as an HTTP/2 stream or a caller-supplied fetch body. Every read is an external completion, as it is for a network body in Node.
type opaqueBodyReadiness struct {
	body    io.ReadCloser
	readers sync.WaitGroup
	close   sync.Once
	err     error
}

func newOpaqueBodyReadiness(body io.ReadCloser) *opaqueBodyReadiness {
	return &opaqueBodyReadiness{body: body}
}

func (readiness *opaqueBodyReadiness) begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	readiness.readers.Go(func() {
		buffer := make([]byte, bufio.MaxScanTokenSize)
		count, err := readiness.body.Read(buffer)
		deliver(bodyReadSignal{data: buffer[:count], err: err})
	})
	return bodyReadSignal{}, false
}

func (readiness *opaqueBodyReadiness) Close() error {
	readiness.close.Do(func() {
		readiness.err = readiness.body.Close()
		readiness.readers.Wait()
	})
	return readiness.err
}

// bodyStream drives a Web ReadableStream from a bodyReadiness source. The stream models undici's response body, whose chunks Node enqueues when socket data arrives.
type bodyStream struct {
	ctx       context.Context
	executor  *continuationExecutor
	readiness bodyReadiness
	stream    *webReadableStream
	reading   bool
	// onCancel runs when the consumer cancels the stream, before the readiness source closes.
	onCancel func()
	started  bool
	// firstReadRounds delays the first buffered chunk by that many reactions.
	firstReadRounds int
	// hops delays every settlement by the reactions a consumer's reader.read() spends behind undici's byte stream; zero for bodies read through values().
	hops bodyReadHops
	// afterData records that the last chunk with bytes waited for the transport.
	afterData bool
}

// bodyReadHops counts the promise reactions between a byte source's readiness and the settlement of the reader.read() promise the consumer awaits. The values depend on how the data reached the stream, not on the consumer.
type bodyReadHops struct {
	// buffered applies to a read whose bytes were already available when the read was issued.
	buffered int
	// data applies to a read that was pending until the transport delivered bytes.
	data int
	// end applies to the end of the body after a buffered read, and endAfterData after a read that waited for the transport.
	end, endAfterData int
}

// settleAfterHops runs settle after hops promise reactions, each queued behind the reactions already ready; zero runs it now.
func settleAfterHops(executor *continuationExecutor, hops int, settle func()) {
	if hops <= 0 {
		settle()
		return
	}
	executor.post(func() { settleAfterHops(executor, hops-1, settle) })
}

func (source *bodyStream) endHops() int {
	if source.afterData {
		return source.hops.endAfterData
	}
	return source.hops.end
}

// newBodyStream returns a byte stream whose pull algorithm reads the source. A chunk the transport already holds is enqueued inside the pull, so the waiting read request settles in the current turn. Every other completion, and every end of stream, is an external reaction, because Node delivers end-of-stream and network bytes from separate macrotasks even when the final bytes were buffered with the last chunk.
func newBodyStream(ctx context.Context, executor *continuationExecutor, readiness bodyReadiness, onCancel func()) *webReadableStream {
	return newBodyStreamWithFirstRead(ctx, executor, readiness, onCancel, 0)
}

// newBodyStreamWithFirstRead is newBodyStream for a body whose first buffered chunk reaches the waiting read firstReadRounds reactions later than a plain ReadableStream's.
func newBodyStreamWithFirstRead(ctx context.Context, executor *continuationExecutor, readiness bodyReadiness, onCancel func(), firstReadRounds int) *webReadableStream {
	return newBodyStreamWithHops(ctx, executor, readiness, onCancel, firstReadRounds, bodyReadHops{})
}

// newBodyStreamWithHops is newBodyStreamWithFirstRead whose settlements trail the source by hops reactions.
func newBodyStreamWithHops(ctx context.Context, executor *continuationExecutor, readiness bodyReadiness, onCancel func(), firstReadRounds int, hops bodyReadHops) *webReadableStream {
	source := &bodyStream{ctx: ctx, executor: executor, readiness: readiness, onCancel: onCancel, firstReadRounds: firstReadRounds, hops: hops}
	source.stream = newWebReadableStream(executor, false)
	source.stream.pull = source.pull
	source.stream.cancelSource = source.cancel
	return source.stream
}

func (source *bodyStream) pull() {
	if source.reading {
		return
	}
	if source.ctx.Err() != nil {
		source.stream.errorController(context.Canceled)
		return
	}
	source.reading = true
	first := !source.started
	source.started = true
	result, ready := source.readiness.begin(func(result bodyReadSignal) {
		source.executor.postExternal(func() {
			hops := source.hops.data
			if len(result.data) == 0 && result.err != nil {
				hops = source.endHops()
			}
			settleAfterHops(source.executor, hops, func() { source.apply(result, true) })
		})
	})
	if !ready {
		return
	}
	if len(result.data) == 0 && errors.Is(result.err, io.EOF) {
		source.executor.postExternal(func() {
			settleAfterHops(source.executor, source.endHops(), func() { source.apply(result, false) })
		})
		return
	}
	if first && len(result.data) > 0 && source.firstReadRounds > 0 {
		postAfter(source.executor, source.firstReadRounds, func() { source.apply(result, false) })
		return
	}
	if len(result.data) > 0 && source.hops.buffered > 0 {
		settleAfterHops(source.executor, source.hops.buffered, func() { source.apply(result, false) })
		return
	}
	source.apply(result, false)
}

// apply delivers one completed read to the stream. A read that waited for the network and finished after cancellation reports the cancellation instead of its result.
func (source *bodyStream) apply(result bodyReadSignal, waited bool) {
	source.reading = false
	// An abort keeps the transport's own rejection when it already carries the cancellation (undici's "This operation was aborted").
	if waited && source.ctx.Err() != nil && !errors.Is(result.err, context.Canceled) {
		result = bodyReadSignal{err: context.Canceled}
	}
	if len(result.data) > 0 {
		source.afterData = waited
		source.stream.enqueue(result.data)
		if result.err != nil {
			// The terminal event is still to come; no further read may start before it.
			source.reading = true
			source.executor.postExternal(func() {
				settleAfterHops(source.executor, source.endHops(), func() { source.finish(result.err) })
			})
		}
		return
	}
	source.finish(result.err)
}

func (source *bodyStream) finish(err error) {
	source.reading = false
	if errors.Is(err, io.EOF) {
		source.stream.closeController()
		return
	}
	source.stream.errorController(err)
}

func (source *bodyStream) cancel(error) *jsPromise[struct{}] {
	if source.onCancel != nil {
		source.onCancel()
	}
	_ = source.readiness.Close()
	return nil
}
