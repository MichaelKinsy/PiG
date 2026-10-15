package ai

// Ports packages/ai/src/api/openai-codex-responses.ts.

// The SSE path (stream, processStream, mapCodexEvents, parseSSE) runs as one executor turn. Pi runs the request IIFE, parseSSE, mapCodexEvents and processResponsesStream as coroutines on one JavaScript thread, and exactly one of them runs at a time, so the provider turn awaits at every point where a coroutine yields, resumes or awaits. Each suspendContinuation is one microtask generation. The differential tests replay the Node oracle in coding/testdata/rpc33-observation/providers/openai-codex-responses and require the same tick for every push.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

const (
	// codexFetchResolutionTicks is the number of microtask generations undici needs to resolve fetch() after the
	// socket data event that carried the response headers.
	codexFetchResolutionTicks = 8
	// codexBufferedReadHops is the read() latency, from the call to the promise resolution, when the body bytes were
	// already available: the promise resolves after two reactions and the awaiting code resumes one generation later.
	codexBufferedReadHops = 2
	// codexPendingReadHops is the same latency from a socket data event that completes a pending read().
	codexPendingReadHops = 3
	// codexCancelOpenTicks and codexCancelClosedTicks are the awaits of reader.cancel() on a readable and on a closed stream.
	codexCancelOpenTicks   = 2
	codexCancelClosedTicks = 1
)

var errCodexAborted = errors.New("Request was aborted")

// postAfter runs fn in the hops-th microtask generation after the caller.
func postAfter(executor *continuationExecutor, hops int, fn func()) {
	executor.post(func() {
		if hops <= 1 {
			fn()
			return
		}
		postAfter(executor, hops-1, fn)
	})
}

// codexBodyReader models the ReadableStreamDefaultReader parseSSE holds over undici's response body.
type codexBodyReader struct {
	ctx       context.Context
	readiness *observedBodyReadiness
	turn      *continuationTurn
	abort     context.CancelFunc
	// ending is set when the last data arrived together with EOF: the stream closes in a later event-loop turn.
	ending bool
	closed bool
	once   sync.Once
}

// newCodexBodyReader reads body through the readiness bridge, so a blocked network read never holds the executor. release closes the body once and releases the request.
func newCodexBodyReader(ctx context.Context, body io.ReadCloser, release func(), turn *continuationTurn, abort context.CancelFunc) *codexBodyReader {
	observed, ok := body.(*observedResponseBody)
	if !ok {
		// A body without a readiness owner is always pending: every read completes as an external event.
		observed = &observedResponseBody{ReadCloser: body, owner: &bodyReadOwner{}, alwaysPending: true}
	}
	readiness := newObservedBodyReadiness(observed)
	readiness.release = release
	return &codexBodyReader{ctx: ctx, readiness: readiness, turn: turn, abort: abort}
}

// read is `await reader.read()` (openai-codex-responses.ts:789). It returns the bytes, or done for a closed stream.
func (reader *codexBodyReader) read() (data []byte, done bool, err error) {
	turn := reader.turn
	executor := turn.executor
	if reader.closed {
		suspendContinuation(turn) // read() on a closed stream is an already resolved promise
		return nil, true, nil
	}
	promise := newContinuationPromise[bodyReadSignal](executor)
	complete := func(hops int, signal bodyReadSignal) {
		switch {
		case reader.ctx.Err() != nil:
			// The abort listener cancels the reader, which resolves the outstanding read as done (:781-783).
			signal = bodyReadSignal{err: io.EOF}
		case len(signal.data) > 0 && errors.Is(signal.err, io.EOF):
			// Node closes the stream in a later event-loop turn even when the final bytes arrived with the data.
			reader.ending = true
			signal.err = nil
		}
		postAfter(executor, hops, func() { promise.resolve(signal) })
	}
	if reader.ending {
		reader.ending = false
		executor.postExternal(func() { complete(codexPendingReadHops, bodyReadSignal{err: io.EOF}) })
	} else {
		signal, ready := reader.readiness.begin(func(signal bodyReadSignal) {
			executor.postExternal(func() { complete(codexPendingReadHops, signal) })
		})
		switch {
		case !ready:
			// The readiness source completes the read through the external queue.
		case len(signal.data) == 0 && errors.Is(signal.err, io.EOF):
			executor.postExternal(func() { complete(codexPendingReadHops, signal) })
		default:
			complete(codexBufferedReadHops, signal)
		}
	}
	result := awaitContinuation(turn, promise)
	switch {
	case result.err == nil:
		return result.data, false, nil
	case errors.Is(result.err, io.EOF):
		reader.closed = true
		return nil, true, nil
	default:
		return nil, false, result.err
	}
}

// cancel is `await reader.cancel()` (:826-828): it releases the network stream and awaits the cancellation promise.
func (reader *codexBodyReader) cancel() {
	ticks := codexCancelOpenTicks
	if reader.closed {
		ticks = codexCancelClosedTicks
	}
	reader.closed = true
	reader.close()
	for range ticks {
		suspendContinuation(reader.turn)
	}
}

// close releases the request and joins the body worker.
func (reader *codexBodyReader) close() {
	reader.once.Do(func() {
		if reader.abort != nil {
			reader.abort()
		}
		_ = reader.readiness.Close()
	})
}

// codexTextDecoder is a TextDecoder("utf-8") used with {stream: true}: it holds an incomplete trailing sequence,
// replaces malformed input with U+FFFD and strips one leading byte order mark.
type codexTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *codexTextDecoder) decode(data []byte, stream bool) string {
	data = append(decoder.pending, data...)
	decoder.pending = nil
	var text strings.Builder
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			if stream {
				decoder.pending = append([]byte(nil), data...)
				break
			}
			text.WriteRune(utf8.RuneError)
			break
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			size = openAIInvalidUTF8Prefix(data)
		}
		data = data[size:]
		if !decoder.started {
			decoder.started = true
			if r == '\ufeff' {
				continue
			}
		}
		text.WriteRune(r)
	}
	return text.String()
}

// decodeTo appends the decoded text of data to dst. Well-formed UTF-8 with no sequence pending is the text itself, without a leading byte order mark and with an incomplete trailing sequence of a streaming call kept for the next one; other data is decoded rune by rune.
func (decoder *codexTextDecoder) decodeTo(dst, data []byte, stream bool) []byte {
	if len(decoder.pending) == 0 {
		cut := len(data)
		if stream {
			cut -= incompleteUTF8Tail(data)
		}
		if utf8.Valid(data[:cut]) {
			text := data[:cut]
			if cut < len(data) {
				decoder.pending = append([]byte(nil), data[cut:]...)
			}
			if cut > 0 && !decoder.started {
				decoder.started = true
				text = bytes.TrimPrefix(text, utf8BOM)
			}
			return append(dst, text...)
		}
	}
	return append(dst, decoder.decode(data, stream)...)
}

// codexRecordSeparator ends an SSE frame in parseSSE.
var codexRecordSeparator = []byte("\n\n")

// codexSSEFrames is parseSSE's buffer and its `buffer.indexOf("\n\n")` loop. The decoded text is appended to one buffer, and the separator is searched for only in the bytes after the last search, so a frame that spans many reads costs its own length.
type codexSSEFrames struct {
	// text[head:] is the decoded text not yet split into frames; no separator starts in text[head:scanFrom].
	text     []byte
	head     int
	scanFrom int
}

// codexFrameBufferReuse is the largest frame buffer kept once its frames are taken.
const codexFrameBufferReuse = 64 << 10

// add appends the decoded text of one read; at the end of the body, a residual frame that is not blank gets its separator.
func (frames *codexSSEFrames) add(decoder *codexTextDecoder, data []byte, done bool) {
	if frames.head > 0 {
		// The frames before head are taken: move the rest to the front so the buffer is reused.
		n := copy(frames.text, frames.text[frames.head:])
		frames.text, frames.scanFrom, frames.head = frames.text[:n], frames.scanFrom-frames.head, 0
		if n == 0 && cap(frames.text) > codexFrameBufferReuse {
			frames.text = nil
		}
	}
	frames.text = decoder.decodeTo(frames.text, data, !done)
	if done && trimJSWhitespace(string(frames.text)) != "" {
		// Treat EOF as terminating the residual SSE frame.
		frames.text = append(frames.text, codexRecordSeparator...)
	}
}

// next takes the next frame that ends in a separator.
func (frames *codexSSEFrames) next() (string, bool) {
	index := bytes.Index(frames.text[frames.scanFrom:], codexRecordSeparator)
	if index < 0 {
		// A separator may straddle the next read by one byte.
		frames.scanFrom = max(frames.head, len(frames.text)-1)
		return "", false
	}
	index += frames.scanFrom
	frame := string(frames.text[frames.head:index])
	frames.head = index + len(codexRecordSeparator)
	frames.scanFrom = frames.head
	return frame, true
}

// codexSSEDecoder is processResponsesStream's `for await (const event of mapCodexEvents(parseSSE(...)))`.
type codexSSEDecoder struct {
	builder *assistantStreamBuilder
	turn    *continuationTurn
	ctx     context.Context
	reader  *codexBodyReader
	text    codexTextDecoder
	frames  codexSSEFrames
	current serverSentEvent
	err     error

	// parseSSE generator state.
	parseDone bool
	draining  bool // inside `while (idx !== -1)`, after a yield
	eof       bool
	// mapCodexEvents generator state.
	mapDone       bool
	mapAtTerminal bool // suspended at the yield of the terminal event
}

func newCodexSSEDecoder(ctx context.Context, reader *codexBodyReader, builder *assistantStreamBuilder) *codexSSEDecoder {
	return &codexSSEDecoder{builder: builder, turn: builder.turn, ctx: ctx, reader: reader}
}

func (decoder *codexSSEDecoder) suspend() { suspendContinuation(decoder.turn) }

// Next is one iteration of processResponsesStream's for-await: it calls mapCodexEvents.next() and awaits its promise.
func (decoder *codexSSEDecoder) Next() bool {
	decoder.builder.publish()
	ok := decoder.mapNext()
	decoder.suspend() // Await(mapCodexEvents.next()) resumes processResponsesStream (openai-responses-shared.ts:530)
	return ok
}

func (decoder *codexSSEDecoder) Event() serverSentEvent { return decoder.current }
func (decoder *codexSSEDecoder) Err() error             { return decoder.err }

// mapNext is mapCodexEvents.next() (openai-codex-responses.ts:725-762).
func (decoder *codexSSEDecoder) mapNext() bool {
	if decoder.mapDone {
		return false
	}
	if decoder.mapAtTerminal {
		// `yield ...; return;` resumes after the terminal event: leaving the for-await closes parseSSE (:756-757).
		decoder.mapDone = true
		decoder.parseReturn()
		decoder.suspend() // AsyncIteratorClose awaits parseSSE.return()
		return false
	}
	for {
		record, ok, err := decoder.parseNext()
		decoder.suspend() // for-await awaits parseSSE.next() (:729)
		if err != nil {
			decoder.mapDone = true
			decoder.err = err
			return false
		}
		if !ok {
			decoder.mapDone = true
			return false
		}
		// mapCodexEvents awaits the observer before it reads the event; a failing observer must not reach WebSocket recovery (ProviderStreamEventCallbackError).
		if err := decoder.builder.observeProviderEvent(record.raw); err != nil {
			decoder.mapDone = true
			decoder.err = err
			decoder.parseReturn()
			decoder.suspend()
			return false
		}
		if record.value == nil {
			// `null.type` throws a TypeError inside the for-await body, which closes parseSSE.
			decoder.mapDone = true
			decoder.err = errors.New("Cannot read properties of null (reading 'type')")
			decoder.parseReturn()
			decoder.suspend()
			return false
		}
		event, isObject := record.value.(map[string]any)
		if !isObject {
			continue // typeof event.type !== "string"
		}
		mapped, mapErr := mapCodexEvent(event, record.raw)
		if mapErr != nil {
			decoder.mapDone = true
			decoder.err = mapErr
			decoder.parseReturn()
			decoder.suspend() // AsyncIteratorClose awaits parseSSE.return() before rethrowing
			return false
		}
		if mapped.skip {
			continue
		}
		if mapped.terminal {
			if response, _ := event["response"].(map[string]any); response != nil {
				if endTurn, ok := response["end_turn"].(bool); ok {
					decoder.builder.partial.EndTurn = new(endTurn) // :743-745
					decoder.builder.publish()
				}
			}
			decoder.mapAtTerminal = true
		}
		decoder.current = serverSentEvent{Data: string(mapped.data)}
		decoder.suspend() // the yield awaits its value
		return true
	}
}

// codexRecord is one parsed SSE data record.
type codexRecord struct {
	value any
	raw   []byte
}

// parseNext is parseSSE.next() (openai-codex-responses.ts:773-830).
func (decoder *codexSSEDecoder) parseNext() (codexRecord, bool, error) {
	if decoder.parseDone {
		return codexRecord{}, false, nil
	}
	for {
		if !decoder.draining {
			if decoder.ctx.Err() != nil {
				return decoder.parseFinish(errCodexAborted) // :786-788
			}
			data, done, err := decoder.reader.read()
			if err != nil {
				return decoder.parseFinish(err)
			}
			if decoder.ctx.Err() != nil {
				return decoder.parseFinish(errCodexAborted) // :790-792
			}
			decoder.frames.add(&decoder.text, data, done)
			decoder.draining = true
			decoder.eof = done
		}
		for {
			chunk, ok := decoder.frames.next()
			if !ok {
				break
			}
			var dataLines []string
			for line := range strings.SplitSeq(chunk, "\n") {
				// upstream: packages/ai/src/api/openai-codex-responses.ts:parseSSE filters "data:" lines itself rather than using a shared SSE decoder.
				if value, ok := strings.CutPrefix(line, "data:"); ok {
					dataLines = append(dataLines, trimJSWhitespace(value))
				}
			}
			if len(dataLines) == 0 {
				continue
			}
			data := trimJSWhitespace(strings.Join(dataLines, "\n"))
			if data == "" || data == "[DONE]" {
				continue
			}
			// One copy of the record serves the syntax check, the decode and the record's raw bytes; neither check keeps or changes it.
			raw := []byte(data)
			var value any
			if err := jsonparse.Validate(raw); err != nil {
				// CodexProtocolError with JSON.parse's message (:811-817).
				return decoder.parseFinish(fmt.Errorf("Invalid Codex SSE JSON: %w", err))
			}
			if err := json.Unmarshal(raw, &value); err != nil {
				return decoder.parseFinish(fmt.Errorf("Invalid Codex SSE JSON: %w", err))
			}
			decoder.suspend() // the yield awaits its value (:810)
			return codexRecord{value: value, raw: raw}, true, nil
		}
		decoder.draining = false
		if decoder.eof {
			return decoder.parseFinish(nil)
		}
	}
}

// parseFinish runs parseSSE's finally block (:823-832) and completes the generator.
func (decoder *codexSSEDecoder) parseFinish(err error) (codexRecord, bool, error) {
	decoder.finishParse()
	return codexRecord{}, false, err
}

func (decoder *codexSSEDecoder) finishParse() {
	decoder.parseDone = true
	decoder.reader.cancel()
}

// parseReturn is parseSSE.return() on a generator suspended at its yield: the resumption awaits the return value,
// then the finally block runs.
func (decoder *codexSSEDecoder) parseReturn() {
	if decoder.parseDone {
		return
	}
	decoder.suspend()
	decoder.finishParse()
}

// finishCodexSSE is the code after processResponsesStream's for-await loop and the stream IIFE's catch and success paths.
func (p *openAIResponsesProvider) finishCodexSSE(ctx context.Context, builder *assistantStreamBuilder, decoder providerSSEDecoder, sawTerminal bool) {
	// processResponsesStream settles, processStream awaits it, and the stream IIFE awaits processStream (openai-codex-responses.ts:475,668).
	suspendContinuation(builder.turn)
	suspendContinuation(builder.turn)
	err := decoder.Err()
	if err == nil && !sawTerminal {
		err = errors.New("OpenAI Responses stream ended before a terminal response event") // openai-responses-shared.ts:758-760
	}
	if err == nil {
		err = unfinishedToolCallError(builder)
	}
	if err != nil {
		reason := StopReasonError
		if ctx.Err() != nil {
			reason = StopReasonAborted
		}
		builder.failUnfinished(reason, err)
		return
	}
	switch {
	case ctx.Err() != nil:
		builder.failUnfinished(StopReasonAborted, errCodexAborted) // :477-479
	case builder.partial.StopReason == StopReasonError:
		builder.failUnfinished(StopReasonError, errors.New(builder.partial.ErrorMessage)) // assertSuccessfulOutput
	default:
		builder.done(builder.partial.StopReason, nil, "")
	}
}
