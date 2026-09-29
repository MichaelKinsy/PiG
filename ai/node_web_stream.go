package ai

import "errors"

// This file models Node 24.19.0 lib/internal/webstreams/readablestream.js over the jsPromise model. Each function names the Node function it mirrors. A body pipeline written over the model consumes the same number of reactions as Pi's fetch-based providers, whose response.body is an undici byte stream read through ReadableStream.values() or getReader().read(). node_web_stream_test.go replays testdata/node-microtasks/webstreams.mjs against real Node.
//
// Not modeled: BYOB readers, tee, pipeTo, high-water marks above zero, the controller's pulling/pullAgain flags, and the start algorithm's one-reaction delay. None of them changes when a read promise settles for a push-style source whose chunks arrive from the transport.

var (
	errWebReaderDetached = errors.New("The reader is not attached to a stream")
	errWebReaderUnbound  = errors.New("The reader is not bound to a ReadableStream")
	errWebStreamLocked   = errors.New("ReadableStream is locked")
)

type webStreamState uint8

const (
	webStreamReadable webStreamState = iota
	webStreamClosed
	webStreamErrored
)

// webReadResult is the {value, done} object a read() or next() promise fulfills with. A rejection carries its reason instead.
type webReadResult struct {
	value []byte
	done  bool
}

// webReadRequest mirrors the kChunk, kClose and kError steps of Node's read request classes.
type webReadRequest interface {
	chunk(value []byte)
	close()
	fail(err error)
}

// webReadableStream is a ReadableStream whose controller is a byte controller (undici's response body) or a default controller. Only the default controller has the buffered fast paths in read() and values().next().
type webReadableStream struct {
	executor          *continuationExecutor
	defaultController bool
	state             webStreamState
	storedError       error
	reader            *webStreamReader
	queue             [][]byte
	closeRequested    bool

	// pull is the controller's pull algorithm. It runs synchronously while a read request waits on an empty queue and may enqueue in the same call.
	pull func()
	// cancelSource is the source's cancel method wrapped by createPromiseCallback1Param (`async (arg) => cancel(arg)`). A nil result is a plain return value; a promise is adopted like `return promise` from an async function. A nil cancelSource is nonOpCancel.
	cancelSource func(reason error) *jsPromise[struct{}]
}

func newWebReadableStream(executor *continuationExecutor, defaultController bool) *webReadableStream {
	return &webReadableStream{executor: executor, defaultController: defaultController}
}

// enqueue is controller.enqueue. Chunks arrive from the transport, never from the consumer.
func (stream *webReadableStream) enqueue(chunk []byte) {
	if stream.closeRequested || stream.state != webStreamReadable {
		return
	}
	// readable{Default,Byte}StreamControllerEnqueue: a waiting read request takes the chunk, otherwise it joins the queue.
	if reader := stream.reader; reader != nil && len(reader.readRequests) > 0 {
		request := reader.readRequests[0]
		reader.readRequests = reader.readRequests[1:]
		request.chunk(chunk)
	} else {
		stream.queue = append(stream.queue, chunk)
	}
	stream.callPullIfNeeded()
}

// closeController is controller.close.
func (stream *webReadableStream) closeController() {
	if stream.closeRequested || stream.state != webStreamReadable {
		return
	}
	stream.closeRequested = true
	if len(stream.queue) == 0 {
		stream.closeStream()
	}
}

// errorController is controller.error.
func (stream *webReadableStream) errorController(err error) {
	if stream.state != webStreamReadable {
		return
	}
	stream.queue = nil
	stream.cancelSource = nil
	stream.pull = nil
	stream.errorStream(err)
}

// callPullIfNeeded is readable{Byte,Default}StreamControllerCallPullIfNeeded for a zero high-water mark: the source is pulled only while a read request waits.
func (stream *webReadableStream) callPullIfNeeded() {
	if stream.state != webStreamReadable || stream.closeRequested || stream.pull == nil {
		return
	}
	if reader := stream.reader; reader != nil && len(reader.readRequests) > 0 {
		stream.pull()
	}
}

// closeStream is readableStreamClose.
func (stream *webReadableStream) closeStream() {
	stream.state = webStreamClosed
	stream.pull = nil
	reader := stream.reader
	if reader == nil {
		return
	}
	requests := reader.readRequests
	reader.readRequests = nil
	for _, request := range requests {
		request.close()
	}
}

// errorStream is readableStreamError.
func (stream *webReadableStream) errorStream(err error) {
	stream.state = webStreamErrored
	stream.storedError = err
	reader := stream.reader
	if reader == nil {
		return
	}
	requests := reader.readRequests
	reader.readRequests = nil
	for _, request := range requests {
		request.fail(err)
	}
}

// dequeue is the shared tail of readableStreamDefaultControllerPullSteps and readableByteStreamControllerFillReadRequestFromQueue: the stream closes before the chunk is handed over.
func (stream *webReadableStream) dequeue() []byte {
	chunk := stream.queue[0]
	stream.queue[0] = nil
	stream.queue = stream.queue[1:]
	if stream.closeRequested && len(stream.queue) == 0 {
		stream.cancelSource = nil
		stream.closeStream()
	} else {
		stream.callPullIfNeeded()
	}
	return chunk
}

// cancelStream is readableStreamCancel.
func (stream *webReadableStream) cancelStream(reason error) *jsPromise[struct{}] {
	switch stream.state {
	case webStreamClosed:
		return jsResolved(stream.executor, struct{}{})
	case webStreamErrored:
		return jsRejected[struct{}](stream.executor, stream.storedError)
	}
	stream.closeStream()
	// controller[kCancel]: resetQueue, run the cancel algorithm, clear the algorithms.
	stream.queue = nil
	result := jsResolved(stream.executor, struct{}{})
	if source := stream.cancelSource; source != nil {
		if returned := source(reason); returned != nil {
			result = newJSPromise[struct{}](stream.executor)
			result.resolve(jsPromiseOperand(returned))
		}
	}
	stream.cancelSource = nil
	// PromisePrototypeThen(result, () => {})
	return jsThen(result, func(struct{}) (jsOperand[struct{}], error) { return jsValue(struct{}{}), nil }, nil)
}

// webStreamReader is a ReadableStreamDefaultReader.
type webStreamReader struct {
	executor     *continuationExecutor
	stream       *webReadableStream
	readRequests []webReadRequest
}

// getReader is ReadableStream.getReader.
func (stream *webReadableStream) getReader() (*webStreamReader, error) {
	if stream.reader != nil {
		return nil, errWebStreamLocked
	}
	reader := &webStreamReader{executor: stream.executor, stream: stream}
	stream.reader = reader
	return reader, nil
}

// releaseGeneric is readableStreamReaderGenericRelease.
func (reader *webStreamReader) releaseGeneric() {
	reader.stream.reader = nil
	reader.stream = nil
}

// read is ReadableStreamDefaultReader.prototype.read.
func (reader *webStreamReader) read() *jsPromise[webReadResult] {
	stream := reader.stream
	if stream == nil {
		return jsRejected[webReadResult](reader.executor, errWebReaderDetached)
	}
	if stream.state == webStreamReadable && stream.defaultController && len(stream.queue) > 0 {
		// Buffered fast path: a settled promise without a read request.
		return jsResolved(stream.executor, webReadResult{value: stream.dequeue()})
	}
	request := &webDefaultReadRequest{promise: newJSPromise[webReadResult](stream.executor)}
	reader.readRequest(request)
	return request.promise
}

// cancel is ReadableStreamDefaultReader.prototype.cancel.
func (reader *webStreamReader) cancel(reason error) *jsPromise[struct{}] {
	if reader.stream == nil {
		return jsRejected[struct{}](reader.executor, errWebReaderDetached)
	}
	return reader.stream.cancelStream(reason)
}

// readRequest is readableStreamDefaultReaderRead followed by the controller's pull steps.
func (reader *webStreamReader) readRequest(request webReadRequest) {
	stream := reader.stream
	switch stream.state {
	case webStreamClosed:
		request.close()
	case webStreamErrored:
		request.fail(stream.storedError)
	case webStreamReadable:
		if len(stream.queue) > 0 {
			request.chunk(stream.dequeue())
			return
		}
		reader.readRequests = append(reader.readRequests, request)
		stream.callPullIfNeeded()
	}
}

// webDefaultReadRequest is DefaultReadRequest.
type webDefaultReadRequest struct {
	promise *jsPromise[webReadResult]
}

func (request *webDefaultReadRequest) chunk(value []byte) {
	request.promise.resolve(jsValue(webReadResult{value: value}))
}

func (request *webDefaultReadRequest) close() {
	request.promise.resolve(jsValue(webReadResult{done: true}))
}

func (request *webDefaultReadRequest) fail(err error) { request.promise.reject(err) }

// webStreamValues is the object returned by ReadableStream.prototype.values. Its next and return are plain functions that return Promises, not async functions, exactly as in Node.
type webStreamValues struct {
	stream  *webReadableStream
	reader  *webStreamReader
	done    bool
	current *jsPromise[webReadResult]
	started bool
}

// values is ReadableStream.prototype.values without preventCancel.
func (stream *webReadableStream) values() (*webStreamValues, error) {
	reader, err := stream.getReader()
	if err != nil {
		return nil, err
	}
	return &webStreamValues{stream: stream, reader: reader}, nil
}

// nextSteps is the nextSteps closure in values().
func (iterator *webStreamValues) nextSteps() *jsPromise[webReadResult] {
	executor := iterator.stream.executor
	if iterator.done {
		return jsResolved(executor, webReadResult{done: true})
	}
	if iterator.reader.stream == nil {
		return jsRejected[webReadResult](executor, errWebReaderUnbound)
	}
	promise := newJSPromise[webReadResult](executor)
	iterator.reader.readRequest(&webIteratorReadRequest{iterator: iterator, promise: promise})
	return promise
}

// chain is PromisePrototypeThen(current, handler, handler), the chaining next() and return() use while a read is in flight.
func (iterator *webStreamValues) chain(handler func() *jsPromise[webReadResult]) *jsPromise[webReadResult] {
	return jsThen(iterator.current,
		func(webReadResult) (jsOperand[webReadResult], error) { return jsPromiseOperand(handler()), nil },
		func(error) (jsOperand[webReadResult], error) { return jsPromiseOperand(handler()), nil })
}

// next is values().next.
func (iterator *webStreamValues) next() *jsPromise[webReadResult] {
	executor := iterator.stream.executor
	if !iterator.started {
		// PromiseResolve() delays the first read one reaction so the controller can start.
		iterator.current = jsResolved(executor, webReadResult{})
		iterator.started = true
	}
	if iterator.current != nil {
		iterator.current = iterator.chain(iterator.nextSteps)
		return iterator.current
	}
	stream := iterator.reader.stream
	if !iterator.done && stream != nil && stream.state == webStreamReadable && stream.defaultController && len(stream.queue) > 0 {
		return jsResolved(executor, webReadResult{value: stream.dequeue()})
	}
	// The read request's chunk step clears iterator.current before this assignment, so a synchronously buffered read leaves its settled promise in current.
	promise := iterator.nextSteps()
	iterator.current = promise
	return promise
}

// returnSteps is values().return.
func (iterator *webStreamValues) returnSteps(reason error) *jsPromise[webReadResult] {
	iterator.started = true
	returnAsync := func() *jsPromise[webReadResult] { return iterator.returnAsync(reason) }
	if iterator.current != nil {
		iterator.current = iterator.chain(returnAsync)
	} else {
		iterator.current = returnAsync()
	}
	return iterator.current
}

// returnAsync is the async function returnSteps in values(). It awaits the cancellation result before it returns.
func (iterator *webStreamValues) returnAsync(reason error) *jsPromise[webReadResult] {
	return runJSAsync(iterator.stream.executor, func(co *jsCoroutine) (jsOperand[webReadResult], error) {
		if iterator.done {
			return jsValue(webReadResult{done: true}), nil
		}
		iterator.done = true
		if iterator.reader.stream == nil {
			return jsOperand[webReadResult]{}, errWebReaderUnbound
		}
		result := iterator.reader.cancel(reason)
		iterator.reader.releaseGeneric()
		if _, err := jsAwait(co, jsPromiseOperand(result)); err != nil {
			return jsOperand[webReadResult]{}, err
		}
		return jsValue(webReadResult{done: true}), nil
	})
}

// webIteratorReadRequest is ReadableStreamAsyncIteratorReadRequest.
type webIteratorReadRequest struct {
	iterator *webStreamValues
	promise  *jsPromise[webReadResult]
}

func (request *webIteratorReadRequest) chunk(value []byte) {
	request.iterator.current = nil
	request.promise.resolve(jsValue(webReadResult{value: value}))
}

func (request *webIteratorReadRequest) close() {
	request.iterator.current = nil
	request.iterator.done = true
	request.iterator.reader.releaseGeneric()
	request.promise.resolve(jsValue(webReadResult{done: true}))
}

func (request *webIteratorReadRequest) fail(err error) {
	request.iterator.current = nil
	request.iterator.done = true
	request.iterator.reader.releaseGeneric()
	request.promise.reject(err)
}
