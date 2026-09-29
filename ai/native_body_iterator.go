package ai

import (
	"context"
	"io"
	"sync"
)

// nativeBodyIterator reads an HTTP/1 response body the way Pi's SDKs do under Node: through undici's byte stream. Each ReadChunk call is one await of values().next(), or of getReader().read() for a reader built by newNativeBodyReader.
type nativeBodyIterator struct {
	turn      *continuationTurn
	readiness *observedBodyReadiness
	values    *webStreamValues
	reader    *webStreamReader
	partial   []byte
	abort     context.CancelFunc
	close     sync.Once
}

// newNativeBodyIterator reads through ReadableStream.prototype.values, as the OpenAI SDK's ReadableStreamToAsyncIterable does under Node.
func newNativeBodyIterator(ctx context.Context, body *observedResponseBody, turn *continuationTurn, abort context.CancelFunc) *nativeBodyIterator {
	readiness := newObservedBodyReadiness(body)
	// A buffered first read of Node 24.19.0's fetch body (undici's ReadableStream behind fetchFinale's pipeThrough) resumes its awaiter two generations later than a plain ReadableStream. Pi's tick oracle fixes the total: providers/azure-openai-responses/ticks.json, buffered rows, first block event 17 generations after start.
	values, err := newBodyStreamWithFirstRead(ctx, turn.executor, readiness, nil, 2).values()
	if err != nil {
		panic(err) // a new stream has no reader
	}
	return &nativeBodyIterator{turn: turn, readiness: readiness, values: values, abort: abort}
}

// newNativeBodyReader reads through getReader().read() and never cancels the stream on close, as a consumer that only releases its reader lock does.
func newNativeBodyReader(ctx context.Context, body *observedResponseBody, turn *continuationTurn, abort context.CancelFunc) *nativeBodyIterator {
	return newNativeBodyReaderWithHops(ctx, body, turn, abort, bodyReadHops{})
}

// newNativeBodyReaderWithHops is newNativeBodyReader for a provider whose measured reader.read() settlements trail undici's byte stream by hops reactions.
func newNativeBodyReaderWithHops(ctx context.Context, body *observedResponseBody, turn *continuationTurn, abort context.CancelFunc, hops bodyReadHops) *nativeBodyIterator {
	readiness := newObservedBodyReadiness(body)
	reader, err := newBodyStreamWithHops(ctx, turn.executor, readiness, nil, 0, hops).getReader()
	if err != nil {
		panic(err) // a new stream has no reader
	}
	return &nativeBodyIterator{turn: turn, readiness: readiness, reader: reader, abort: abort}
}

func (reader *nativeBodyIterator) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(reader.partial) == 0 {
		chunk, err := reader.ReadChunk()
		if err != nil {
			return 0, err
		}
		reader.partial = chunk
	}
	count := copy(buffer, reader.partial)
	reader.partial = reader.partial[count:]
	return count, nil
}

func (reader *nativeBodyIterator) ReadChunk() ([]byte, error) {
	if len(reader.partial) != 0 {
		chunk := reader.partial
		reader.partial = nil
		return chunk, nil
	}
	var read *jsPromise[webReadResult]
	if reader.reader != nil {
		read = reader.reader.read()
	} else {
		read = reader.values.next()
	}
	result, err := jsAwait(reader.turn, jsPromiseOperand(read))
	switch {
	case err != nil:
		return nil, err
	case result.done:
		return nil, io.EOF
	}
	return result.value, nil
}

// Close is the for-await exit: a loop that stops before the stream ends calls values().return(), which cancels the stream and awaits the result. A stream that already ended or failed has nothing to cancel.
func (reader *nativeBodyIterator) Close() error {
	var err error
	reader.close.Do(func() {
		if reader.abort != nil {
			reader.abort()
		}
		if reader.values != nil && !reader.values.done {
			_, err = jsAwait(reader.turn, jsPromiseOperand(reader.values.returnSteps(nil)))
		}
		if closeErr := reader.readiness.Close(); err == nil {
			err = closeErr
		}
	})
	return err
}

func suspendContinuation(turn *continuationTurn) {
	resolved := newContinuationPromise[struct{}](turn.executor)
	resolved.resolve(struct{}{})
	awaitContinuation(turn, resolved)
}

func (builder *assistantStreamBuilder) responseTurn(body func() error) error {
	if !builder.managed {
		return body()
	}
	return builder.stream.responseContinuation(func(turn *continuationTurn) error {
		builder.turn = turn
		defer func() { builder.turn = nil }()
		return body()
	})
}

func (stream *AssistantMessageEventStream) responseContinuation(body func(*continuationTurn) error) error {
	stream.mu.Lock()
	executor := stream.executor
	stream.mu.Unlock()
	if executor == nil {
		return body(nil)
	}
	turn := &continuationTurn{executor: executor, permit: make(chan bool, 1), canceled: make(chan struct{})}
	executor.postExternal(turn.grant)
	var err error
	turn.run(func(turn *continuationTurn) {
		observation := &StreamObservation{turn: turn}
		executor.mu.Lock()
		turn.producer = observation
		executor.mu.Unlock()
		stream.mu.Lock()
		stream.producer = turn
		stream.mu.Unlock()
		defer func() {
			stream.mu.Lock()
			stream.producer = nil
			stream.mu.Unlock()
			observation.end()
			executor.mu.Lock()
			turn.producer = nil
			executor.mu.Unlock()
		}()
		err = body(turn)
	})
	return err
}

func newObservedProviderBuilder(ctx context.Context, api API, provider, model string) *assistantStreamBuilder {
	ctx, executor := withContinuationExecutor(ctx)
	builder := newAssistantStreamBuilder(ctx, api, provider, model)
	builder.stream.executor = executor
	return builder
}
