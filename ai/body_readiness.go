package ai

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"iter"
	"net"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// bodyReadiness is the transport side of the readiness bridge. It reports whether a body read completes in the current turn or becomes an external completion, using only what the transport knows: bytes its own buffer already holds are ready; a read that must wait for the network is pending. No sleep, goroutine schedule, or channel length decides it.
type bodyReadiness interface {
	// begin starts the only outstanding read. When the transport already holds the result, begin returns it with ready true. Otherwise begin returns ready false and calls deliver exactly once, from any goroutine, when the read finishes.
	begin(deliver func(bodyReadSignal)) (result bodyReadSignal, ready bool)
	// Close abandons the body and joins every goroutine the readiness source owns.
	Close() error
}

// bodyReadSize is the most one body read asks for.
const bodyReadSize = bufio.MaxScanTokenSize

var bodyReadBuffers = sync.Pool{New: func() any {
	buffer := make([]byte, bodyReadSize)
	return &buffer
}}

// bodyReadBuffer is the buffer of one body's reads: taken from the pool at its first read and returned when the body fails, ends or closes, so
// a collection between two reads of a stream does not cost the next read a new buffer, and back-to-back responses share one. A body has one
// read outstanding at a time, so its reads share it.
type bodyReadBuffer struct {
	buffer *[]byte
}

// read reads once into the buffer and returns a copy of the bytes read, so a chunk the stream queues costs its own length rather than the read
// size. A read that fails or ends the body returns the buffer to the pool.
func (buffer *bodyReadBuffer) read(body io.Reader) ([]byte, error) {
	if buffer.buffer == nil {
		buffer.buffer = bodyReadBuffers.Get().(*[]byte)
	}
	count, err := body.Read(*buffer.buffer)
	data := []byte{}
	if count > 0 {
		data = bytes.Clone((*buffer.buffer)[:count])
	}
	if err != nil {
		buffer.release()
	}
	return data, err
}

// release returns the buffer to the pool once no read can use it.
func (buffer *bodyReadBuffer) release() {
	if buffer.buffer != nil {
		//perf:ok fixed-size pool: every buffer is bodyReadSize and Read never grows it, so a Put cannot retain a larger one
		bodyReadBuffers.Put(buffer.buffer)
		buffer.buffer = nil
	}
}

// observedBodyReadiness reads an HTTP/1 response body. Each read runs in a coroutine that the caller resumes: a read that the transport's buffers serve completes in the caller's turn with no goroutine hand-off. The connection announces a network wait through bodyReadOwner before it blocks; the announcement suspends the read there and returns pending to the caller, and waitBody, the one goroutine the source owns, resumes the read, blocks for the network and delivers the result.
type observedBodyReadiness struct {
	body   *observedResponseBody
	read   bodyReadOperation
	buffer bodyReadBuffer
	// next resumes the read coroutine: it starts a read, or finishes one suspended at its network wait. Calls never overlap: the caller resumes a read only when none is outstanding, and waitBody resumes only the one handed to it.
	next func() (bodyReadSignal, bool)
	stop func()
	// waits hands a read suspended at its network wait to waitBody with its delivery. Only one read is outstanding, so a send never blocks.
	waits  chan func(bodyReadSignal)
	joined chan struct{}
	// release, when set, closes the body in place of body.Close: a reader whose request owns the body's close.
	release func()
	close   sync.Once
	err     error
}

func newObservedBodyReadiness(body *observedResponseBody) *observedBodyReadiness {
	readiness := &observedBodyReadiness{body: body, waits: make(chan func(bodyReadSignal), 1), joined: make(chan struct{})}
	readiness.next, readiness.stop = iter.Pull(readiness.reads)
	//portlint:allow golifetime waitBody ends when Close closes waits, and Close waits on joined
	go readiness.waitBody()
	return readiness
}

// reads is the read coroutine. It yields each read's result, and a pending signal when the connection announces that the read waits for the network. A body that failed or ended is not read again: the transport may already serve another request on the connection, whose reads must not reach this body's owner.
//
//perf:hot
func (readiness *observedBodyReadiness) reads(yield func(bodyReadSignal) bool) {
	readiness.read.yield = yield
	readiness.read.owner = goroutineid.Current()
	// The coroutine ends when Close stops it, after waitBody delivered any read that was waiting; no read runs after it.
	defer readiness.buffer.release()
	var failed error
	for {
		signal := bodyReadSignal{err: failed}
		if failed == nil {
			readiness.read.announced.Store(false)
			readiness.body.begin(&readiness.read)
			signal.data, signal.err = readiness.buffer.read(readiness.body)
			readiness.body.clear(&readiness.read)
			failed = signal.err
		}
		if !yield(signal) {
			return
		}
	}
}

// waitBody finishes each read that waits for the network and delivers it. A delivery may drain the executor's jobs on this goroutine, and a job may start the next read: that read runs in the coroutine, and if it waits too it is queued on waits for this loop.
func (readiness *observedBodyReadiness) waitBody() {
	//portlint:allow doubleclose only waitBody closes joined, once, when it returns
	defer close(readiness.joined)
	for deliver := range readiness.waits {
		signal, _ := readiness.next()
		deliver(signal)
	}
}

//perf:hot
func (readiness *observedBodyReadiness) begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	signal, ok := readiness.next()
	if !ok {
		return bodyReadSignal{err: net.ErrClosed}, true
	}
	if !signal.pending {
		return signal, true
	}
	//perf:ok one hand-off per network read that must wait (pending); buffered reads return above without a hop
	readiness.waits <- deliver
	return bodyReadSignal{}, false
}

// Close closes the body, which fails a read blocked on the network, joins waitBody after it delivers that read, and ends the coroutine.
func (readiness *observedBodyReadiness) Close() error {
	readiness.close.Do(func() {
		if readiness.release != nil {
			readiness.release()
		} else {
			readiness.err = readiness.body.Close()
		}
		close(readiness.waits)
		<-readiness.joined
		readiness.stop()
	})
	return readiness.err
}

// opaqueBodyReadiness reads a body that cannot report its buffer state, such as an HTTP/2 stream or a caller-supplied fetch body. Every read is an external completion, as it is for a network body in Node.
type opaqueBodyReadiness struct {
	body    io.ReadCloser
	buffer  bodyReadBuffer
	readers sync.WaitGroup
	close   sync.Once
	err     error
}

func newOpaqueBodyReadiness(body io.ReadCloser) *opaqueBodyReadiness {
	return &opaqueBodyReadiness{body: body}
}

func (readiness *opaqueBodyReadiness) begin(deliver func(bodyReadSignal)) (bodyReadSignal, bool) {
	readiness.readers.Go(func() {
		data, err := readiness.buffer.read(readiness.body)
		deliver(bodyReadSignal{data: data, err: err})
	})
	return bodyReadSignal{}, false
}

func (readiness *opaqueBodyReadiness) Close() error {
	readiness.close.Do(func() {
		readiness.err = readiness.body.Close()
		readiness.readers.Wait()
		readiness.buffer.release()
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
	// deliver and settle are deliverPending and settlePending, bound once so that a read allocates no closure for its completion.
	deliver func(bodyReadSignal)
	settle  func()
	// pendingResult carries a read that waited for the network from deliverPending to its external reaction. Only one read is outstanding.
	pendingResult bodyReadSignal
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
	source.deliver, source.settle = source.deliverPending, source.settlePending
	source.stream = newWebReadableStream(executor, false)
	source.stream.pull = source.pull
	source.stream.cancelSource = source.cancel
	return source.stream
}

// deliverPending completes a read that waited for the network: the readiness source calls it from its own goroutine, and the result reaches the stream as an external reaction.
func (source *bodyStream) deliverPending(result bodyReadSignal) {
	source.pendingResult = result
	source.executor.postExternal(source.settle)
}

func (source *bodyStream) settlePending() {
	result := source.pendingResult
	source.pendingResult = bodyReadSignal{}
	hops := source.hops.data
	if len(result.data) == 0 && result.err != nil {
		hops = source.endHops()
	}
	if hops <= 0 {
		source.apply(result, true)
		return
	}
	settleAfterHops(source.executor, hops, func() { source.apply(result, true) })
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
	result, ready := source.readiness.begin(source.deliver)
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
