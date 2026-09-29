package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// This file models the promise chain between a Google Generative AI response's headers and the chunks Pi's `for await (const chunk of googleStream)` receives, at the granularity of promise reactions. Pi runs `client.models.generateContentStream(params)` from @google/genai 2.21.0, whose async functions, `.then` chains and tslib generators (google_sdk_generator.go) each add reactions; the order in which they run relative to the consumer of the event stream decides which partial message that consumer observes. Line numbers refer to packages/ai/src/api/google-generative-ai.ts (Pi) and dist/node/index.mjs (SDK).

// Node 24.19.0 constants that the Go transport cannot derive. They are measured by testdata/rpc33-observation/providers/google-generative-ai/undici.mjs and asserted against that probe's recorded output.
const (
	// googleUndiciFetchRounds is when `fetch()` settles after the socket read that carries the response headers.
	googleUndiciFetchRounds = 7
	// googleUndiciBufferedReadRounds is when the first `reader.read()` settles after the call, for a chunk that arrived with the headers.
	googleUndiciBufferedReadRounds = 2
	// googleUndiciPendingReadRounds is when a pending `reader.read()` settles after the socket read that carries its chunk. The end of the body uses the same latency.
	googleUndiciPendingReadRounds = 3
	// googleUndiciJSONRounds is when `new Response(text).json()` settles after the call.
	googleUndiciJSONRounds = 2
)

// afterReactions runs step after n reactions; zero runs it now.
func afterReactions(executor *continuationExecutor, n int, step func()) {
	if n == 0 {
		step()
		return
	}
	executor.post(func() { afterReactions(executor, n-1, step) })
}

// googleFetchResponse is the `Response` `fetch()` resolves with. Only its body is read.
type googleFetchResponse struct {
	body *webReadableStream
}

// googleHTTPResponse is the SDK's HttpResponse over `new Response(text)` (index.mjs:2632).
type googleHTTPResponse struct {
	text string
}

// json is Response.json() on a string body. The returned promise settles rounds reactions after the call; a parse failure rejects.
func (response *googleHTTPResponse) json(executor *continuationExecutor, rounds int) *tslibPromise {
	promise := newTslibPromise(executor)
	afterReactions(executor, rounds, func() {
		var chunk geminiStreamChunk
		if err := json.Unmarshal([]byte(response.text), &chunk); err != nil {
			promise.reject(fmt.Errorf("google: invalid SSE JSON: %w", err))
			return
		}
		promise.resolve(&chunk)
	})
	return promise
}

// googleSDKStream is what one generateContentStream call built: the generator Pi iterates and the inner generator it wraps. Closing releases both coroutines.
type googleSDKStream struct {
	outer *tslibAsyncIterator
	inner *tslibAsyncIterator
}

func (stream *googleSDKStream) close() {
	if stream.outer != nil {
		stream.outer.close()
	}
	if stream.inner != nil {
		stream.inner.close()
	}
}

// googleGenerateContentStream is `client.models.generateContentStream(params)` (google-generative-ai.ts:100) once fetch() has been called; its promise fulfills with the tslib generator Pi iterates. Every hop below is one reaction, and F is the round in which fetched settles.
func googleGenerateContentStream(executor *continuationExecutor, fetched *tslibPromise, jsonRounds int, session *googleSDKStream) *tslibPromise {
	// passthrough is a `then` whose handler returns its value, or a `.catch` that a fulfilled promise skips.
	passthrough := func(promise *tslibPromise) *tslibPromise {
		return promise.then(func(value any) (any, error) { return value, nil }, nil)
	}
	// adopt is what an async function does when it returns a promise: the derived promise adopts it, which costs the thenable job and one more reaction.
	adopt := func(promise *tslibPromise) *tslibPromise {
		derived := newTslibPromise(executor)
		derived.resolve(promise)
		return derived
	}

	// F+1: `response = await fetch(...)` in runFetch resumes and returns the response (index.mjs:13877, 13891).
	runFetch := passthrough(fetched)
	// F+2: apiCall is `async` and ends with `return runFetch()` (index.mjs:13868, 13910).
	apiCall := adopt(runFetch)
	// F+3..F+5: streamApiCall's `.then(async (response) => { await throwErrorIfNotOK(response); return this.processStreamResponse(response); })` (index.mjs:13765-13769). The handler runs at F+3, its await resumes at F+4 and the derived promise adopts the handler's promise, settling at F+5.
	handled := apiCall.then(func(value any) (any, error) {
		result := newTslibPromise(executor)
		// `throwErrorIfNotOK` is async and returns for an ok response; its await costs one reaction.
		tslibPromiseResolve(executor, nil).then(func(any) (any, error) {
			session.inner = googleProcessStreamResponse(executor, value.(*googleFetchResponse), jsonRounds)
			result.resolve(session.inner)
			return nil, nil
		}, nil)
		return result, nil
	}, nil)
	// F+6: `.catch(...)` passes a fulfilled promise through (index.mjs:13771).
	caught := passthrough(handled)
	// F+7: `return this.apiCall(...).then(...).catch(...)` in async streamApiCall (index.mjs:13765).
	streamApiCall := adopt(caught)
	// F+8: `return this.streamApiCall(...)` in async requestStream (index.mjs:13738).
	requestStream := adopt(streamApiCall)
	// F+9: `return response.then(function (apiResponse) { return __asyncGenerator(...) })` in generateContentStreamInternal (index.mjs:15830-15832, 15837-15855).
	generator := requestStream.then(func(value any) (any, error) {
		session.outer = googleGenerateContentStreamInternal(executor, value.(*tslibAsyncIterator), jsonRounds)
		return session.outer, nil
	}, nil)
	// F+10: generateContentStreamInternal is async and returns that promise (index.mjs:15768).
	internal := adopt(generator)
	// F+11: `return await this.generateContentStreamInternal(transformedParams)` in generateContentStream (index.mjs:15313-15314).
	return passthrough(internal)
}

// googleRetryGoogleRequest is retryGoogleRequest(request, options) for a request that does not fail: two async wrappers, each of which awaits the request and returns (google-shared.ts:499-501 and utils/provider-retry.ts:87). Pi's `await` of the result resumes one reaction after it settles, 14 reactions after fetched settled, which is when Pi pushes start.
func googleRetryGoogleRequest(request *tslibPromise) *tslibPromise {
	passthrough := func(promise *tslibPromise) *tslibPromise {
		return promise.then(func(value any) (any, error) { return value, nil }, nil)
	}
	// F+12: `async () => { try { return await request(); } ... }`, then F+13: `return await request()` in retryProviderRequest.
	return passthrough(passthrough(request))
}

// googleRead is ReadableStreamDefaultReader.prototype.read (node_web_stream.go) as a JavaScript promise: the fast path for a buffered default-controller chunk settles at once, otherwise a read request waits for the chunk.
func googleRead(reader *webStreamReader) *tslibPromise {
	promise := newTslibPromise(reader.executor)
	stream := reader.stream
	if stream == nil {
		promise.reject(errWebReaderDetached)
		return promise
	}
	if stream.state == webStreamReadable && stream.defaultController && len(stream.queue) > 0 {
		promise.resolve(webReadResult{value: stream.dequeue()})
		return promise
	}
	reader.readRequest(&googleReadRequest{promise: promise})
	return promise
}

// googleReadRequest is DefaultReadRequest over a JavaScript promise.
type googleReadRequest struct{ promise *tslibPromise }

func (request *googleReadRequest) chunk(value []byte) {
	request.promise.resolve(webReadResult{value: value})
}
func (request *googleReadRequest) close()         { request.promise.resolve(webReadResult{done: true}) }
func (request *googleReadRequest) fail(err error) { request.promise.reject(err) }

// googleProcessStreamResponse is ApiClient.processStreamResponse (index.mjs:13780-13860): a tslib async generator over the response body reader that yields one HttpResponse per SSE record.
func googleProcessStreamResponse(executor *continuationExecutor, response *googleFetchResponse, jsonRounds int) *tslibAsyncIterator {
	return newTslibAsyncGenerator(executor, func(y *tslibYield) (any, error) {
		reader, err := response.body.getReader() // index.mjs:13782
		if err != nil {
			return nil, err
		}
		defer reader.releaseGeneric() // finally { reader.releaseLock(); } (index.mjs:13858)
		decoder := googleTextDecoder{}
		buffer := ""
		for {
			// `const { done, value } = yield __await(reader.read());` (index.mjs:13793)
			value, err := y.Await(googleRead(reader))
			if err != nil {
				return nil, err
			}
			read := value.(webReadResult)
			if read.done {
				if trimJSWhitespace(buffer) != "" {
					return nil, errors.New("Incomplete JSON segment at the end") // index.mjs:13796
				}
				return nil, nil
			}
			chunkString := decoder.decode(read.value)
			// A raw chunk that is a JSON error envelope with a 4xx/5xx code is an ApiError (index.mjs:13800-13820).
			if err := googleRawChunkError([]byte(chunkString)); err != nil {
				return nil, err
			}
			buffer += chunkString
			for {
				index, length := googleSSEDelimiter(buffer)
				if index < 0 {
					break
				}
				event := trimJSWhitespace(buffer[:index])
				buffer = buffer[index+length:]
				data, ok := strings.CutPrefix(event, "data:")
				if !ok {
					continue
				}
				// `yield yield __await(new HttpResponse(partialResponse))` (index.mjs:13854)
				if _, err := y.YieldAwaited(&googleHTTPResponse{text: trimJSWhitespace(data)}); err != nil {
					return nil, err
				}
			}
		}
	})
}

// googleGenerateContentStreamInternal is the async generator in Models.generateContentStreamInternal (index.mjs:15830-15855). It awaits each HttpResponse's json() and yields the parsed GenerateContentResponse; an exception inside the loop closes the inner generator before it is rethrown.
func googleGenerateContentStreamInternal(executor *continuationExecutor, apiResponse *tslibAsyncIterator, jsonRounds int) *tslibAsyncIterator {
	return newTslibAsyncGenerator(executor, func(y *tslibYield) (any, error) {
		// for (... apiResponse_2_1 = yield __await(apiResponse_2.next()) ...) { ... } with the compiled try/catch/finally (index.mjs:15833-15853)
		err := y.ForAwait(tslibAsyncValues(apiResponse), func(value any) (bool, error) {
			chunk := value.(*googleHTTPResponse)
			// `generateContentResponseFromMldev((yield __await(chunk.json())), params)` (index.mjs:15838)
			parsed, err := y.Await(chunk.json(executor, jsonRounds))
			if err != nil {
				return false, err
			}
			// `yield yield __await(typedResp)` (index.mjs:15844)
			_, err = y.YieldAwaited(parsed)
			return false, err
		})
		return nil, err
	})
}

// googleSSEDelimiter finds the earliest of the SDK's record delimiters "\n\n", "\r\r" and "\r\n\r\n" (index.mjs:13803, 13820-13834). The SDK asks indexOf for each delimiter and keeps the smallest index; no two delimiters can start at the same index, so scanning the line-break candidates in order finds the same one without re-reading the buffer once per delimiter.
func googleSSEDelimiter(buffer string) (index, length int) {
	for start := 0; ; {
		found := strings.IndexAny(buffer[start:], "\r\n")
		if found < 0 {
			return -1, 0
		}
		found += start
		switch rest := buffer[found:]; {
		case strings.HasPrefix(rest, "\n\n"), strings.HasPrefix(rest, "\r\r"):
			return found, 2
		case strings.HasPrefix(rest, "\r\n\r\n"):
			return found, 4
		}
		start = found + 1
	}
}

// googleTextDecoder is `new TextDecoder('utf-8')` with `decode(value, { stream: true })`: a code point split between chunks is held for the next call and invalid bytes become U+FFFD.
type googleTextDecoder struct {
	partial []byte
}

func (decoder *googleTextDecoder) decode(chunk []byte) string {
	data := slices.Concat(decoder.partial, chunk)
	decoder.partial = nil
	// A trailing incomplete sequence is at most three bytes.
	for tail := 1; tail <= utf8.UTFMax-1 && tail <= len(data); tail++ {
		if start := len(data) - tail; utf8.RuneStart(data[start]) {
			if !utf8.FullRune(data[start:]) {
				decoder.partial = append([]byte(nil), data[start:]...)
				data = data[:start]
			}
			break
		}
	}
	var text strings.Builder
	for len(data) > 0 {
		character, size := utf8.DecodeRune(data)
		if character == utf8.RuneError && size == 1 {
			text.WriteRune(utf8.RuneError)
		} else {
			text.Write(data[:size])
		}
		data = data[size:]
	}
	return text.String()
}

// googleUndiciBody feeds the response body into a Web ReadableStream the way Node's undici does: the stream is a byte stream that pulls its source only while a read waits. A chunk the Go transport has already buffered settles the read googleUndiciBufferedReadRounds after the call. A chunk that needs a socket read completes as an external completion, like the macrotask that delivers it in Node, and settles googleUndiciPendingReadRounds later. The end of the body is a process.nextTick callback in undici: it runs when the microtask queue is empty, before the next macrotask.
//
// The transport side is the readiness bridge (body_readiness.go): a read either completes inside begin, or through deliver as an external reaction.
type googleUndiciBody struct {
	ctx       context.Context
	executor  *continuationExecutor
	stream    *webReadableStream
	readiness bodyReadiness
	abort     context.CancelFunc
	closer    sync.Once

	buffered int
	pending  int
	reading  bool
	finished bool
	aborted  bool
	// disarm removes the abort watcher armed while a read request waits.
	disarm func()
}

func newGoogleUndiciBody(ctx context.Context, executor *continuationExecutor, readiness bodyReadiness, abort context.CancelFunc) *googleUndiciBody {
	source := &googleUndiciBody{
		ctx: ctx, executor: executor, readiness: readiness, abort: abort,
		stream:   newWebReadableStream(executor, false),
		buffered: googleUndiciBufferedReadRounds,
		pending:  googleUndiciPendingReadRounds,
	}
	source.stream.pull = source.pull
	return source
}

// pull is the byte stream's pull algorithm: it starts one transport read for the waiting read request.
func (source *googleUndiciBody) pull() {
	if source.reading || source.aborted {
		return
	}
	if err := contextTransportError(source.ctx); err != nil {
		// A fetch that was aborted errors its body stream, even when the end of the body is already known: a read after that rejects at once.
		source.abortWaiting()
		return
	}
	// A JavaScript abort() errors the body stream in the job that calls it, so a waiting read is watched for cancellation.
	source.arm()
	if source.finished {
		return
	}
	source.reading = true
	result, ready := source.readiness.begin(func(signal bodyReadSignal) {
		source.executor.postExternal(func() { source.arrive(signal, true) })
	})
	if ready {
		source.arrive(result, false)
	}
}

func (source *googleUndiciBody) arm() {
	if source.disarm == nil {
		source.disarm = source.executor.watch(source.ctx, source.abortWaiting)
	}
}

func (source *googleUndiciBody) disarmWatch() {
	if source.disarm != nil {
		source.disarm()
		source.disarm = nil
	}
}

// abortWaiting errors the body stream because the fetch was aborted. Whatever the transport still delivers is discarded.
func (source *googleUndiciBody) abortWaiting() {
	if source.aborted {
		return
	}
	source.disarm = nil
	source.aborted, source.finished, source.reading = true, true, false
	source.stream.errorController(googleFetchAbortError(contextTransportError(source.ctx)))
}

// arrive handles a finished transport read. A read that had to wait for the socket runs as an external completion and reaches the stream later than one the transport had already buffered.
func (source *googleUndiciBody) arrive(signal bodyReadSignal, external bool) {
	if source.aborted {
		return
	}
	source.reading = false
	rounds := source.buffered
	if external {
		rounds = source.pending
	}
	if contextTransportError(source.ctx) != nil {
		source.abortWaiting()
		return
	}
	if len(signal.data) > 0 {
		data := signal.data
		afterReactions(source.executor, rounds, func() {
			if source.aborted {
				return
			}
			source.disarmWatch()
			source.stream.enqueue(data)
		})
		if signal.err != nil {
			source.end(signal.err, false)
		}
		return
	}
	if signal.err != nil {
		source.end(signal.err, external)
		return
	}
	// A read that returned nothing and no error: read again.
	source.pull()
}

// end delivers the end of the body or a transport failure. Inside an external completion it starts at once; otherwise it is a nextTick callback.
func (source *googleUndiciBody) end(err error, external bool) {
	source.finished = true
	deliver := func() {
		afterReactions(source.executor, source.pending, func() {
			if source.aborted {
				return
			}
			if contextTransportError(source.ctx) != nil {
				// The fetch was aborted before the end of the body was delivered: the stream is already errored.
				source.abortWaiting()
				return
			}
			source.disarmWatch()
			if errors.Is(err, io.EOF) {
				source.stream.closeController()
				return
			}
			source.stream.errorController(err)
		})
	}
	if external {
		deliver()
		return
	}
	source.executor.postTick(deliver)
}

// googleFetchAbortError is what an aborted fetch's body rejects with: the signal's reason, which for a plain abort() is Node's AbortError, "This operation was aborted".
func googleFetchAbortError(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.New("This operation was aborted")
	}
	return err
}

// close stops the reader goroutine and closes the response.
func (source *googleUndiciBody) close() {
	source.closer.Do(func() {
		source.disarmWatch()
		if source.abort != nil {
			source.abort()
		}
		_ = source.readiness.Close()
	})
}
