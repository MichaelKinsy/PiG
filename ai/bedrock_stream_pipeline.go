//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"errors"
	"io"
)

// This file runs Pi's Bedrock stream loop (packages/ai/src/api/bedrock-converse-stream.ts:280-330) over a Node http.IncomingMessage as one executor turn. Each `await`, `yield` and `for await` step of the SDK's async generator chain is one suspendContinuation (an already-settled await) or one awaitContinuation (a pending promise), in the order the JavaScript source resumes them. The reaction counts follow the JavaScript source and are checked against the Node oracle in coding/testdata/rpc33-observation/providers/bedrock-converse-stream/pi.json (bedrock_observation_test.go).
//
// The chain, top to bottom, with the source of each generator (paths under @smithy/core dist-es/submodules/event-streams):
//
//	Pi loop  for await (const item of response.stream!)                 bedrock-converse-stream.ts:296
//	W        the object deserializeEventStream returns                  EventStreamSerde.js:780-790
//	S        SmithyMessageDecoderStream.asyncIterator                   eventstream-codec/SmithyMessageDecoderStream.js:14-23
//	M        MessageDecoderStream.asyncIterator                         eventstream-codec/MessageDecoderStream.js:8-14
//	C        getChunkedStream's iterator                                eventstream-serde-universal/getChunkedStream.js:24-62
//	R        Readable.prototype[Symbol.asyncIterator]                   Node 24.19.0 lib/internal/streams/readable.js (node_readable_iterator.go)
//
// Every generator is pulled by exactly one consumer, one item at a time, so the chain collapses into straight-line code: a pull resumes the generators synchronously from the top down to the first one that must wait, and each await after that is the next reaction in the queue.

// bedrockClientSendPrefixHops is the number of reactions between the response's arrival and the first pull of the chain: the HTTP handler's promise and the SDK middleware stack up to the deserializer's first read of `response.body`. Probe: bedrock-converse-stream/chain.mjs records `Readable[Symbol.asyncIterator]` at job 4 without Pi's response middleware.
const bedrockClientSendPrefixHops = 4

// bedrockResponseHookPrefixHops is what Pi's deserialize middleware adds to the prefix when the caller passes `onResponse` (bedrock-converse-stream.ts:510-523: `await next(args)`, then `await onResponse(...)`). Probe: chain.mjs records the first read at job 6 with the middleware.
const bedrockResponseHookPrefixHops = 2

// bedrockClientSendReturnHops is the number of reactions from the first event's arrival at deserializeEventStream (`await asyncIterator.next()`, EventStreamSerde.js:764-765) to `await client.send(...)` settling in Pi (bedrock-converse-stream.ts:284): protocol.deserializeEventStream, deserializeHttpMessage, deserializerMiddleware and every middleware layer's `await next(args)` on the way out. Probe: chain.mjs records `send resolved` at job 37 with the first read at job 6 and the first event resolving 12 reactions later (bedrockFirstEventHops, this file's transcription of C, M, S and the per-event function), so 37 = 6 + 12 + 19.
const bedrockClientSendReturnHops = 19

// bedrockNoResponseHookReturnHops is what Pi adds after `send` when it passes no `onResponse`: `await options?.onResponse?.(...)` awaits undefined (bedrock-converse-stream.ts:289-296). Probe: chain.mjs.
const bedrockNoResponseHookReturnHops = 1

// bedrockSmithyChain is the pull-based generator chain between the Readable and Pi's loop.
type bedrockSmithyChain struct {
	turn     *continuationTurn
	readable *nodeReadable
	reader   *jsAsyncGenerator[[]byte] // R, created by C's first pull (getChunkedStream.js:24 `source[Symbol.asyncIterator]()`)
	chunker  bedrockMessageChunker
	queue    [][]byte // messages of the current chunk C has not yielded
	pending  error    // an error C throws after the messages queued before it
	ended    bool     // C's generator completed
}

func (chain *bedrockSmithyChain) hop() { suspendContinuation(chain.turn) }

func (chain *bedrockSmithyChain) hops(count int) {
	for range count {
		chain.hop()
	}
}

// chunkedNext resumes C. It returns the next complete message, done at the end of the source, or the error C throws.
func (chain *bedrockSmithyChain) chunkedNext() (message []byte, done bool, err error) {
	for {
		if len(chain.queue) > 0 {
			message = chain.queue[0]
			chain.queue = chain.queue[1:]
			chain.hop() // getChunkedStream.js:455 `yield currentMessage`: Await(operand).
			return message, false, nil
		}
		if chain.pending != nil {
			err, chain.pending, chain.ended = chain.pending, nil, true
			return nil, false, err // a throw rejects the request at once
		}
		if chain.ended {
			return nil, true, nil
		}
		if chain.reader == nil {
			chain.reader = chain.readable.asyncIterator()
		}
		result, failure := jsAwait(chain.turn, jsPromiseOperand(chain.reader.next())) // getChunkedStream.js:419 `await sourceIterator.next()`
		switch {
		case failure != nil:
			chain.ended = true
			return nil, false, failure
		case result.done:
			chain.ended = true
			if err := chain.chunker.finish(); err != nil {
				return nil, false, err
			}
			return nil, true, nil // bare `return;` does not await
		}
		chain.queue, chain.pending = chain.chunker.feed(result.value)
	}
}

// decodedNext resumes M: `for await (const bytes of this.options.inputStream)` over C, decoding each message.
func (chain *bedrockSmithyChain) decodedNext() (message *bedrockEventMessage, done bool, err error) {
	bytes, done, err := chain.chunkedNext()
	chain.hop() // MessageDecoderStream.js:341 the for-await awaits C.next()
	if err != nil || done {
		return nil, done, err
	}
	message, err = decodeBedrockEventMessage(bytes)
	if err != nil {
		// The body throws inside the loop: AsyncIteratorClose(C) resumes C's suspended yield with a return completion (Await) and awaits its result.
		chain.hops(2)
		return nil, false, err
	}
	chain.hop() // MessageDecoderStream.js:343 `yield decoded`: Await(operand).
	return message, false, nil
}

// smithyNext resumes S. Events the ConverseStream union does not model are dropped and S pulls again (SmithyMessageDecoderStream.js:19-20).
func (chain *bedrockSmithyChain) smithyNext() (item bedrockStreamItem, done bool, err error) {
	for {
		message, ended, failure := chain.decodedNext()
		chain.hop() // SmithyMessageDecoderStream.js:376 the for-await awaits M.next()
		if failure != nil || ended {
			return bedrockStreamItem{}, ended, failure
		}
		item, hops, failure := chain.deserialize(message)
		chain.hops(hops)
		if failure != nil {
			// The body throws inside the loop: AsyncIteratorClose(M) resumes M's yield with a return completion, M closes C the same way, and each level awaits the close (two levels, two awaits each).
			chain.hops(4)
			return bedrockStreamItem{}, false, failure
		}
		if item.event == nil {
			continue
		}
		chain.hop() // SmithyMessageDecoderStream.js:380 `yield deserialized`: Await(operand).
		return item, false, nil
	}
}

// deserialize is `await this.options.deserializer(message)` (getMessageUnmarshaller composed with deserializeEventStream's per-event function). hops counts the awaits from the call to S resuming, including S's own.
func (chain *bedrockSmithyChain) deserialize(message *bedrockEventMessage) (item bedrockStreamItem, hops int, err error) {
	item, err = bedrockEventDeserializer(message)
	messageType := message.headers[":message-type"].value
	switch {
	case messageType == "error" || messageType == "":
		// getUnmarshalledStream.js:483-490: the unmarshaller throws before it awaits; only S's await remains.
		return bedrockStreamItem{}, 1, err
	case messageType == "exception":
		if bedrockStreamException(message.headers[":exception-type"].value, message.body) == nil {
			// `$unknown`: the per-event function returns at once (EventStreamSerde.js:758).
			return bedrockStreamItem{}, 2, err
		}
		return bedrockStreamItem{}, bedrockEventMemberHops(message), err
	case item.event == nil && err == nil:
		// `$unknown`: EventStreamSerde.js:758 returns without readEventMember; the unmarshaller returns undefined.
		return bedrockStreamItem{}, 2, nil
	}
	return item, bedrockEventMemberHops(message), err
}

// bedrockEventMemberHops is the await chain of a modeled member: `await this.deserializer.read(...)` in readEventMember (EventStreamSerde.js:807; skipped for an empty body), `await this.readEventMember(...)` in the per-event function (EventStreamSerde.js:751), `await deserializer(event)` in the unmarshaller (getUnmarshalledStream.js:505) and `await this.options.deserializer(message)` in S (SmithyMessageDecoderStream.js:377).
func bedrockEventMemberHops(message *bedrockEventMessage) int {
	if len(message.body) == 0 {
		return 3
	}
	return 4
}

// wNext resumes W, the async generator deserializeEventStream returns, after its first event.
func (chain *bedrockSmithyChain) wNext() (item bedrockStreamItem, done bool, err error) {
	item, done, err = chain.smithyNext()
	chain.hop() // EventStreamSerde.js:785 `await asyncIterator.next()`
	if err != nil || done {
		return bedrockStreamItem{}, done, err // W throws or returns: the request settles at once
	}
	chain.hop() // EventStreamSerde.js:789 `yield value`: Await(operand).
	return item, false, nil
}

// bedrockBodyFeeder is the transport side of an http.IncomingMessage: it pushes the bytes the connection delivers into the Readable, in the reaction that receives them. A read the transport already buffered completes inside the current reaction, as Node's parser pushes a whole socket read before any microtask runs; a read that waits for the network completes as an external reaction.
type bedrockBodyFeeder struct {
	ctx       context.Context
	executor  *continuationExecutor
	readable  *nodeReadable
	readiness bodyReadiness
	ended     bool
}

func (feeder *bedrockBodyFeeder) fill() {
	for !feeder.ended {
		result, ready := feeder.readiness.begin(func(signal bodyReadSignal) {
			feeder.executor.postExternal(func() {
				feeder.deliver(signal)
				feeder.fill()
			})
		})
		if !ready {
			return
		}
		feeder.deliver(result)
	}
}

func (feeder *bedrockBodyFeeder) deliver(signal bodyReadSignal) {
	if len(signal.data) > 0 {
		feeder.readable.push(signal.data, false)
	}
	switch {
	case signal.err == nil:
	case errors.Is(signal.err, io.EOF):
		feeder.ended = true
		feeder.readable.push(nil, true)
	default:
		feeder.ended = true
		err := signal.err
		if feeder.ctx.Err() != nil {
			// A read that fails because the request was canceled is the destroyed response's `aborted`, whichever of the two completions runs first.
			err = errBedrockBodyAborted
		}
		feeder.readable.errorOrDestroy(err)
	}
}

// runBedrockPipeline is the response half of the Pi loop for a body the provider reads itself. It runs inside builder.responseTurn.
func (p *BedrockProvider) runBedrockPipeline(ctx context.Context, turn *continuationTurn, builder *assistantStreamBuilder, body *observedResponseBody, requestID string, hasResponseHook bool) {
	executor := turn.executor
	readiness := newObservedBodyReadiness(body)
	defer func() { _ = readiness.Close() }()
	readable := newNodeReadable(executor, nodeReadableOptions{incomingMessage: true})
	feeder := &bedrockBodyFeeder{ctx: ctx, executor: executor, readable: readable, readiness: readiness}
	stopAbort := context.AfterFunc(ctx, func() {
		executor.postExternal(func() {
			feeder.ended = true
			readable.errorOrDestroy(errBedrockBodyAborted)
		})
	})
	defer stopAbort()

	state := newBedrockStreamState(p, builder)
	chain := &bedrockSmithyChain{turn: turn, readable: readable}
	fail := func(err error) {
		state.finalizeBlocks()
		if ctx.Err() != nil {
			// The catch block records `aborted` with formatBedrockError(error) and no failure diagnostic (bedrock-converse-stream.ts:317-326).
			builder.fail(StopReasonAborted, errors.New(formatBedrockError(err)))
			return
		}
		failBedrockResponse(ctx, builder, err, requestID, true)
	}

	// The response arrived: the transport delivers whatever it already holds before any microtask runs.
	feeder.fill()
	chain.hops(bedrockClientSendPrefixHops)
	if hasResponseHook {
		chain.hops(bedrockResponseHookPrefixHops)
	}

	// deserializeEventStream reads the first event inside client.send (EventStreamSerde.js:764-765).
	first, done, err := chain.smithyNext()
	chain.hop() // EventStreamSerde.js:765 `await asyncIterator.next()`
	chain.hops(bedrockClientSendReturnHops)
	if !hasResponseHook {
		chain.hops(bedrockNoResponseHookReturnHops)
	}
	if err != nil {
		fail(err)
		return
	}
	if done {
		state.finalizeBlocks()
		state.finish(ctx, nil, requestID)
		return
	}
	// The first pull of W yields the event it already holds (EventStreamSerde.js:782): Await(operand), then Pi's for-await awaits the request.
	chain.hops(2)
	item := first
	for {
		err := builder.observeProviderEventData(item.event) // bedrock-converse-stream.ts:297
		if err == nil {
			err = state.handleItem(item)
		}
		// Pi's handlers mutate the shared output in place; a consumer's continuation sees the state at the end of this synchronous segment, before the next await.
		builder.publish()
		if err != nil {
			// Pi throws inside its for-await: AsyncIteratorClose resumes W's yield with a return completion (Await) and awaits the result.
			chain.hops(2)
			fail(err)
			return
		}
		var next bedrockStreamItem
		next, done, err = chain.wNext()
		chain.hop() // bedrock-converse-stream.ts:296 the for-await awaits W.next()
		if err != nil {
			fail(err)
			return
		}
		if done {
			break
		}
		item = next
	}
	if ctx.Err() != nil {
		fail(errBedrockRequestWasAborted) // bedrock-converse-stream.ts:333-335
		return
	}
	state.finalizeBlocks()
	state.finish(ctx, nil, requestID)
}

// errBedrockBodyAborted is what Node's HTTP client reports on an aborted response stream: IncomingMessage emits 'aborted' with this message when the request is destroyed.
var errBedrockBodyAborted = errors.New("aborted")

// errBedrockRequestWasAborted is the error Pi throws after the loop when the signal is aborted (bedrock-converse-stream.ts:334).
var errBedrockRequestWasAborted = errors.New("Request was aborted")
