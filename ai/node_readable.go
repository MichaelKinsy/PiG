package ai

import "errors"

// This file models the part of Node 24.19.0 lib/internal/streams/readable.js that a byte-mode Readable's Symbol.asyncIterator exercises, together with the tick and event ordering of its end-of-stream watcher. @smithy/core event streams read the AWS SDK's Node HTTP response (an http.IncomingMessage) through `source[Symbol.asyncIterator]()`, which is createAsyncIterator below. Each function names the Node function it mirrors. node_readable_test.go replays testdata/node-microtasks/readable.mjs against real Node.
//
// Not modeled: object mode, string decoding, pipe, flowing mode (resume, 'data' listeners), unshift, and Duplex streams. The transport is the only writer: push arrives from external reactions, or before the consumer starts when the transport already holds the bytes.

var (
	errNodeStreamPushAfterEOF = errors.New("stream.push() after EOF")
	errNodeStreamPrematureEnd = errors.New("Premature close")
	errNodeStreamAbort        = errors.New("The operation was aborted")
)

// nodeReadAll is Readable.read() without a size: n is NaN.
const nodeReadAll = -1

// nodeReadableOptions selects the http.IncomingMessage flavor of Readable. IncomingMessage starts with readingMore set and its _read clears that flag once (lib/_http_incoming.js).
type nodeReadableOptions struct {
	incomingMessage bool
	highWaterMark   int
	noAutoDestroy   bool
}

type nodeListener struct{ fn func(error) }

// nodeReadable is a byte-mode Readable. Its state flags carry Node's names.
type nodeReadable struct {
	executor      *continuationExecutor
	highWaterMark int
	buffer        [][]byte
	length        int

	ended             bool
	endEmitted        bool
	reading           bool
	sync              bool
	needReadable      bool
	emittedReadable   bool
	readingMore       bool
	destroyed         bool
	closed            bool
	closeEmitted      bool
	errorEmitted      bool
	readableListening bool
	hasFlowing        bool
	flowing           bool
	autoDestroy       bool
	errored           error

	incomingMessage bool
	consuming       bool
	events          map[string][]*nodeListener

	// readSource is Readable._read. It may push synchronously.
	readSource func()
}

func newNodeReadable(executor *continuationExecutor, options nodeReadableOptions) *nodeReadable {
	readable := &nodeReadable{
		executor:        executor,
		highWaterMark:   options.highWaterMark,
		sync:            true,
		autoDestroy:     !options.noAutoDestroy,
		incomingMessage: options.incomingMessage,
		events:          map[string][]*nodeListener{},
	}
	if readable.highWaterMark == 0 {
		readable.highWaterMark = 64 * 1024
	}
	if options.incomingMessage {
		readable.readingMore = true
	}
	return readable
}

func (readable *nodeReadable) nextTick(callback func()) { readable.executor.postTick(callback) }

// on is Readable.prototype.on for the events this model emits.
func (readable *nodeReadable) on(event string, fn func(error)) *nodeListener {
	listener := &nodeListener{fn: fn}
	readable.events[event] = append(readable.events[event], listener)
	if event == "readable" && !readable.endEmitted && !readable.readableListening {
		readable.readableListening = true
		readable.needReadable = true
		readable.hasFlowing = true
		readable.flowing = false
		readable.emittedReadable = false
		if readable.length != 0 {
			readable.emitReadable()
		} else if !readable.reading {
			readable.nextTick(func() { readable.read(0) }) // nReadingNextTick
		}
	}
	return listener
}

// off is Readable.prototype.removeListener.
func (readable *nodeReadable) off(event string, listener *nodeListener) {
	listeners := readable.events[event]
	for i, candidate := range listeners {
		if candidate == listener {
			readable.events[event] = append(listeners[:i:i], listeners[i+1:]...)
			break
		}
	}
	if event == "readable" {
		readable.nextTick(readable.updateReadableListening)
	}
}

func (readable *nodeReadable) updateReadableListening() {
	readable.readableListening = len(readable.events["readable"]) > 0
	if !readable.readableListening {
		readable.hasFlowing = false
		readable.flowing = false
	}
}

func (readable *nodeReadable) emit(event string, err error) {
	listeners := append([]*nodeListener(nil), readable.events[event]...)
	for _, listener := range listeners {
		listener.fn(err)
	}
}

func (readable *nodeReadable) canPushMore() bool {
	return !readable.ended && (readable.length < readable.highWaterMark || readable.length == 0)
}

// push is Readable.prototype.push (readableAddChunkPushByteMode) for a Buffer chunk. A nil chunk with eof set is push(null).
func (readable *nodeReadable) push(chunk []byte, eof bool) bool {
	if eof {
		readable.reading = false
		readable.onEofChunk()
		return false
	}
	if len(chunk) == 0 {
		readable.reading = false
		readable.maybeReadMore()
		return readable.canPushMore()
	}
	if readable.ended {
		readable.errorOrDestroy(errNodeStreamPushAfterEOF)
		return false
	}
	if readable.destroyed || readable.errored != nil {
		return false
	}
	readable.reading = false
	readable.addChunk(chunk)
	return readable.canPushMore()
}

func (readable *nodeReadable) addChunk(chunk []byte) {
	// Flowing mode with a 'data' listener is not modeled, so the chunk always joins the buffer.
	readable.length += len(chunk)
	readable.buffer = append(readable.buffer, chunk)
	if readable.needReadable {
		readable.emitReadable()
	}
	readable.maybeReadMore()
}

func (readable *nodeReadable) onEofChunk() {
	if readable.ended {
		return
	}
	readable.ended = true
	if readable.sync {
		readable.emitReadable()
		return
	}
	readable.needReadable = false
	readable.emittedReadable = true
	readable.emitReadable_()
}

func (readable *nodeReadable) emitReadable() {
	readable.needReadable = false
	if !readable.emittedReadable {
		readable.emittedReadable = true
		readable.nextTick(readable.emitReadable_)
	}
}

func (readable *nodeReadable) emitReadable_() {
	if !readable.destroyed && readable.errored == nil && (readable.length != 0 || readable.ended) {
		readable.emit("readable", nil)
		readable.emittedReadable = false
	}
	if !readable.flowing && !readable.ended && readable.length <= readable.highWaterMark {
		readable.needReadable = true
	}
	readable.flow()
}

func (readable *nodeReadable) maybeReadMore() {
	if !readable.readingMore && !readable.reading {
		readable.readingMore = true
		readable.nextTick(readable.maybeReadMore_)
	}
}

func (readable *nodeReadable) maybeReadMore_() {
	for !readable.reading && !readable.ended && (readable.length < readable.highWaterMark || (readable.flowing && readable.length == 0)) {
		length := readable.length
		readable.read(0)
		if length == readable.length {
			break
		}
	}
	readable.readingMore = false
}

func (readable *nodeReadable) flow() {
	for readable.flowing && readable.read(nodeReadAll) != nil {
	}
}

func (readable *nodeReadable) howMuchToRead(n int) int {
	all := n == nodeReadAll
	if (!all && n <= 0) || (readable.length == 0 && readable.ended) {
		return 0
	}
	if all {
		if readable.flowing && readable.length != 0 {
			return len(readable.buffer[0])
		}
		return readable.length
	}
	if n <= readable.length {
		return n
	}
	if readable.ended {
		return readable.length
	}
	return 0
}

// fromList is fromList for a read of the whole buffer, the only size read(undefined) and read(0) request.
func (readable *nodeReadable) fromList() []byte {
	if readable.length == 0 {
		return nil
	}
	var chunk []byte
	if len(readable.buffer) == 1 {
		chunk = readable.buffer[0]
	} else {
		chunk = make([]byte, 0, readable.length)
		for _, part := range readable.buffer {
			chunk = append(chunk, part...)
		}
	}
	readable.buffer = nil
	return chunk
}

// read is Readable.prototype.read for n = undefined (nodeReadAll) and n = 0. A nil result is null.
func (readable *nodeReadable) read(n int) []byte {
	original := n
	if n != 0 {
		readable.emittedReadable = false
	}
	if n == 0 && readable.needReadable && ((readable.highWaterMark != 0 && readable.length >= readable.highWaterMark) || (readable.highWaterMark == 0 && readable.length > 0) || readable.ended) {
		if readable.length == 0 && readable.ended {
			readable.endReadable()
		} else {
			readable.emitReadable()
		}
		return nil
	}
	n = readable.howMuchToRead(n)
	if n == 0 && readable.ended {
		if readable.length == 0 {
			readable.endReadable()
		}
		return nil
	}
	doRead := readable.needReadable
	if readable.length == 0 || readable.length-n < readable.highWaterMark {
		doRead = true
	}
	if !readable.reading && !readable.ended && !readable.destroyed && readable.errored == nil && doRead {
		readable.reading = true
		readable.sync = true
		if readable.length == 0 {
			readable.needReadable = true
		}
		readable.callRead()
		readable.sync = false
		if !readable.reading {
			n = readable.howMuchToRead(original)
		}
	}
	var ret []byte
	if n > 0 {
		ret = readable.fromList()
	}
	if ret == nil {
		if readable.length <= readable.highWaterMark {
			readable.needReadable = true
		}
		n = 0
	} else {
		readable.length -= n
	}
	if readable.length == 0 {
		if !readable.ended {
			readable.needReadable = true
		}
		if original != n && readable.ended {
			readable.endReadable()
		}
	}
	return ret
}

// callRead is Readable._read. http.IncomingMessage._read only clears readingMore on its first call; the socket resume it requests is the transport's concern.
func (readable *nodeReadable) callRead() {
	if readable.incomingMessage && !readable.consuming {
		readable.readingMore = false
		readable.consuming = true
	}
	if readable.readSource != nil {
		readable.readSource()
	}
}

func (readable *nodeReadable) endReadable() {
	if !readable.endEmitted {
		readable.ended = true
		readable.nextTick(readable.endReadableNT)
	}
}

func (readable *nodeReadable) endReadableNT() {
	if readable.errored == nil && !readable.closeEmitted && !readable.endEmitted && readable.length == 0 {
		readable.endEmitted = true
		readable.emit("end", nil)
		if readable.autoDestroy {
			readable.destroy(nil)
		}
	}
}

// errorOrDestroy is destroyImpl.errorOrDestroy.
func (readable *nodeReadable) errorOrDestroy(err error) {
	if readable.destroyed {
		return
	}
	if readable.autoDestroy {
		readable.destroy(err)
		return
	}
	if err != nil && readable.errored == nil {
		readable.errored = err
	}
	readable.emitErrorNT(err)
}

// destroy is destroyImpl.destroy with the default Readable._destroy, which calls back synchronously.
func (readable *nodeReadable) destroy(err error) {
	if readable.destroyed {
		return
	}
	if err != nil && readable.errored == nil {
		readable.errored = err
	}
	readable.destroyed = true
	readable.closed = true
	if err != nil {
		readable.nextTick(func() {
			readable.emitErrorNT(err)
			readable.emitCloseNT()
		})
		return
	}
	readable.nextTick(readable.emitCloseNT)
}

func (readable *nodeReadable) emitErrorNT(err error) {
	if readable.errorEmitted {
		return
	}
	readable.errorEmitted = true
	readable.emit("error", err)
}

func (readable *nodeReadable) emitCloseNT() {
	readable.closeEmitted = true
	readable.emit("close", nil)
}

// isReadable is streams/utils isReadable for a Readable: readable && !readableFinished.
func (readable *nodeReadable) isReadable() bool {
	return !readable.destroyed && !readable.errorEmitted && !readable.endEmitted && !readable.readableFinished(true)
}

// readableFinished is isReadableFinished; strict false also accepts an ended, empty buffer.
func (readable *nodeReadable) readableFinished(strict bool) bool {
	if readable.errored != nil {
		return false
	}
	return readable.endEmitted || (!strict && readable.ended && readable.length == 0)
}

// destroyer is destroyImpl.destroyer.
func (readable *nodeReadable) destroyer(err error) {
	if readable.destroyed {
		return
	}
	if err == nil && readable.isReadable() {
		err = errNodeStreamAbort
	}
	readable.destroy(err)
}

// nodeEndOfStream is eos(stream, { writable: false }, callback) for a Readable.
func nodeEndOfStream(readable *nodeReadable, callback func(error)) (cleanup func()) {
	called := false
	once := func(err error) {
		if !called {
			called = true
			callback(err)
		}
	}
	willEmitClose := readable.autoDestroy && !readable.closed
	readableFinished := readable.readableFinished(false)
	closed := readable.closed
	onEnd := func(error) {
		readableFinished = true
		if readable.destroyed {
			willEmitClose = false
		}
		if willEmitClose {
			return
		}
		once(nil)
	}
	onError := func(err error) { once(err) }
	onClose := func(error) {
		closed = true
		if readable.errored != nil {
			once(readable.errored)
			return
		}
		if !readableFinished && !readable.readableFinished(false) {
			once(errNodeStreamPrematureEnd)
			return
		}
		once(nil)
	}
	onClosed := func() {
		if readable.errored != nil {
			once(readable.errored)
			return
		}
		once(nil)
	}
	end := readable.on("end", onEnd)
	failure := readable.on("error", onError)
	closeListener := readable.on("close", onClose)
	switch {
	case closed:
		readable.nextTick(func() { onClose(nil) })
	case readable.errorEmitted:
		if !willEmitClose {
			readable.nextTick(onClosed)
		}
	case !willEmitClose && (readableFinished || !readable.isReadable()):
		readable.nextTick(onClosed)
	}
	return func() {
		called = true
		readable.off("end", end)
		readable.off("error", failure)
		readable.off("close", closeListener)
	}
}
