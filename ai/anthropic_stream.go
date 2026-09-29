package ai

// Ports packages/ai/src/api/anthropic-messages.ts:iterateSseMessages, iterateAnthropicEvents and the for-await loop of stream().
//
// The Go code runs on the provider's executor turn. Each JavaScript await, yield and Promise adoption that separates the body reader from the provider's loop is a suspend(): one promise reaction queued behind everything already ready. The tick order is checked against Node 24.19 and 26.7 by TestAnthropicMicrotaskTrace over coding/testdata/rpc33-observation/providers/anthropic-messages/pi.json.

import (
	"context"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// The chunk source is the body's ReadableStream reader: ReadChunk is `await reader.read()`, including the reactions Node spends before the read settles.

// anthropicReaderSource adapts an ordinary reader. It has no observable read readiness.
type anthropicReaderSource struct {
	reader io.Reader
	buffer []byte
}

func (source *anthropicReaderSource) ReadChunk() ([]byte, error) {
	if source.buffer == nil {
		source.buffer = make([]byte, 32*1024)
	}
	count, err := source.reader.Read(source.buffer)
	return source.buffer[:count], err
}

// anthropicPublishingSource publishes the producer's state before every read releases the executor, so a consumer that runs while the body is pending sees the mutations made since the last push.
type anthropicPublishingSource struct {
	openAIStreamChunkReader
	builder *assistantStreamBuilder
}

func (source anthropicPublishingSource) ReadChunk() ([]byte, error) {
	source.builder.publishPending()
	return source.openAIStreamChunkReader.ReadChunk()
}

var errAnthropicRequestAborted = errors.New("Request was aborted")

// anthropicSSEState is SseDecoderState.
type anthropicSSEState struct {
	event string
	data  []string
	raw   []string
}

// flush is flushSseEvent.
func (state *anthropicSSEState) flush() (serverSentEvent, bool) {
	if state.event == "" && len(state.data) == 0 {
		return serverSentEvent{}, false
	}
	event := serverSentEvent{Event: state.event, Data: strings.Join(state.data, "\n"), Raw: append([]string(nil), state.raw...)}
	state.event, state.data, state.raw = "", nil, nil
	return event, true
}

// decode is decodeSseLine.
func (state *anthropicSSEState) decode(line string) (serverSentEvent, bool) {
	if line == "" {
		return state.flush()
	}
	state.raw = append(state.raw, line)
	if strings.HasPrefix(line, ":") {
		return serverSentEvent{}, false
	}
	field, value, found := strings.Cut(line, ":")
	if !found {
		value = ""
	}
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		state.event = value
	case "data":
		state.data = append(state.data, value)
	}
	return serverSentEvent{}, false
}

// anthropicConsumeLine is consumeLine. A carriage return at the end of the buffer ends its line; a line feed that arrives in the next chunk then ends an empty line.
func anthropicConsumeLine(text string) (line, rest string, ok bool) {
	index := strings.IndexAny(text, "\r\n")
	if index < 0 {
		return "", "", false
	}
	next := index + 1
	if text[index] == '\r' && next < len(text) && text[next] == '\n' {
		next++
	}
	return text[:index], text[next:], true
}

// webTextDecoder is a TextDecoder with the default UTF-8 label and ignoreBOM false, called with {stream: true} per chunk and once without options at the end.
type webTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *webTextDecoder) decode(data []byte, stream bool) string {
	data = append(decoder.pending, data...)
	decoder.pending = nil
	var text strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if stream && !utf8.FullRune(data) {
				decoder.pending = append([]byte(nil), data...)
				break
			}
			// A maximal subpart of an ill-formed sequence becomes one replacement character.
			text.WriteRune(utf8.RuneError)
			data = data[openAIInvalidUTF8Prefix(data):]
			decoder.started = true
			continue
		}
		wasStarted := decoder.started
		decoder.started = true
		data = data[size:]
		if !wasStarted && r == '\uFEFF' {
			continue
		}
		text.WriteRune(r)
	}
	return text.String()
}

// anthropicSSEReader is iterateSseMessages. Its events are computed when a chunk arrives and yielded one per next(); the generator's internal state has no observable effect between yields.
type anthropicSSEReader struct {
	ctx     context.Context
	source  openAIStreamChunkReader
	suspend func()

	decoder webTextDecoder
	state   anthropicSSEState
	buffer  string
	queue   []serverSentEvent
	readErr error
	ended   bool
	closed  bool
}

func (reader *anthropicSSEReader) drainLines(final bool) {
	for {
		line, rest, ok := anthropicConsumeLine(reader.buffer)
		if !ok {
			break
		}
		reader.buffer = rest
		if event, ok := reader.state.decode(line); ok {
			reader.queue = append(reader.queue, event)
		}
	}
	if !final {
		return
	}
	if len(reader.buffer) > 0 {
		if event, ok := reader.state.decode(reader.buffer); ok {
			reader.queue = append(reader.queue, event)
		}
	}
	if event, ok := reader.state.flush(); ok {
		reader.queue = append(reader.queue, event)
	}
}

// next is one call of iterateSseMessages().next(): an event (its yield awaits the value), completion, or a thrown error.
func (reader *anthropicSSEReader) next() (serverSentEvent, bool, error) {
	for {
		if len(reader.queue) > 0 {
			event := reader.queue[0]
			reader.queue[0] = serverSentEvent{}
			reader.queue = reader.queue[1:]
			reader.suspend() // `yield event` awaits its operand (anthropic-messages.ts:437,449,457,463).
			return event, true, nil
		}
		if reader.ended || reader.closed {
			reader.closed = true
			return serverSentEvent{}, false, nil
		}
		if reader.ctx.Err() != nil {
			reader.closed = true
			return serverSentEvent{}, false, errAnthropicRequestAborted // anthropic-messages.ts:422-424
		}
		var data []byte
		var err error
		if reader.readErr != nil {
			err, reader.readErr = reader.readErr, nil
		} else {
			data, err = reader.source.ReadChunk() // anthropic-messages.ts:426 `await reader.read()`
		}
		if len(data) > 0 && err != nil {
			reader.readErr = err
			err = nil
		}
		switch {
		case err == nil:
			reader.buffer += reader.decoder.decode(data, true)
			reader.drainLines(false)
		case errors.Is(err, io.EOF):
			reader.buffer += reader.decoder.decode(nil, false)
			reader.drainLines(true)
			reader.ended = true
		default:
			reader.closed = true
			return serverSentEvent{}, false, err
		}
	}
}

// close is the return() the consumer's AsyncIteratorClose calls on a generator suspended at a yield: the return value is awaited before the finally block runs (anthropic-messages.ts:466 `reader.releaseLock()`).
func (reader *anthropicSSEReader) close() {
	if reader.closed {
		return
	}
	reader.closed = true
	reader.suspend()
}

// anthropicMessageEvents is ANTHROPIC_MESSAGE_EVENTS.
var anthropicMessageEvents = map[string]bool{
	"message_start": true, "message_delta": true, "message_stop": true,
	"content_block_start": true, "content_block_delta": true, "content_block_stop": true,
}

// anthropicEventReader is iterateAnthropicEvents over anthropicSSEReader, consumed by stream()'s for-await.
type anthropicEventReader struct {
	sse     *anthropicSSEReader
	suspend func()

	sawMessageStart bool
	sawMessageEnd   bool
	finished        bool
}

func newAnthropicEventReader(ctx context.Context, source openAIStreamChunkReader, suspend func()) *anthropicEventReader {
	return &anthropicEventReader{
		sse:     &anthropicSSEReader{ctx: ctx, source: source, suspend: suspend},
		suspend: suspend,
	}
}

// next is one iteration of stream()'s `for await (const event of iterateAnthropicEvents(...))`: it includes the reactions from the generator chain and the loop's own await of the result (anthropic-messages.ts:601).
func (reader *anthropicEventReader) next() (serverSentEvent, bool, error) {
	event, ok, err := reader.generate()
	reader.suspend() // The provider's for-await awaits iterateAnthropicEvents().next().
	if err != nil || !ok {
		reader.finished = true
	}
	return event, ok, err
}

func (reader *anthropicEventReader) generate() (serverSentEvent, bool, error) {
	if reader.finished {
		return serverSentEvent{}, false, nil
	}
	for {
		sse, ok, err := reader.sse.next()
		reader.suspend() // `for await (const sse of iterateSseMessages(...))` awaits next() (anthropic-messages.ts:481).
		if err != nil {
			return serverSentEvent{}, false, err // A throwing iterator is not closed.
		}
		if !ok {
			break
		}
		if sse.Event == "error" {
			reader.closeSSE()
			return serverSentEvent{}, false, errors.New(sse.Data)
		}
		if !anthropicMessageEvents[sse.Event] {
			continue
		}
		if _, err := anthropicParseJSON(sse.Data); err != nil {
			reader.closeSSE()
			return serverSentEvent{}, false, &anthropicSSEParseError{sse: sse, cause: err}
		}
		switch sse.Event {
		case "message_start":
			reader.sawMessageStart = true
		case "message_stop":
			reader.sawMessageEnd = true
		}
		reader.suspend() // `yield event` awaits its operand (anthropic-messages.ts:497).
		return sse, true, nil
	}
	if reader.sawMessageStart && !reader.sawMessageEnd {
		return serverSentEvent{}, false, errors.New("Anthropic stream ended before message_stop")
	}
	return serverSentEvent{}, false, nil
}

// closeSSE is AsyncIteratorClose on the SSE generator: it awaits the generator's return() promise.
func (reader *anthropicEventReader) closeSSE() {
	reader.sse.close()
	reader.suspend()
}

// close is AsyncIteratorClose on this generator after the provider's loop body threw: return() resumes the suspended yield with an awaited return value, closes the inner iterator, and the loop awaits the returned promise.
func (reader *anthropicEventReader) close() {
	if reader.finished {
		return
	}
	reader.finished = true
	reader.suspend()
	reader.closeSSE()
	reader.suspend()
}

type anthropicSSEParseError struct {
	sse   serverSentEvent
	cause error
}

func (err *anthropicSSEParseError) Error() string {
	return "Could not parse Anthropic SSE event " + err.sse.Event + ": " + err.cause.Error() + "; data=" + err.sse.Data + "; raw=" + strings.Join(err.sse.Raw, `\n`)
}

func (err *anthropicSSEParseError) Unwrap() error { return err.cause }

// anthropicParseJSON is parseJsonWithRepair (packages/ai/src/utils/json-parse.ts:85-95): it returns the text that parses, repaired when the original does not, or V8's JSON.parse message for the text that was last tried.
func anthropicParseJSON(data string) (string, error) {
	err := jsonparse.Validate([]byte(data))
	if err == nil {
		return data, nil
	}
	repaired := repairJSON(data)
	if repaired == data {
		return "", err
	}
	if err := jsonparse.Validate([]byte(repaired)); err != nil {
		return "", err
	}
	return repaired, nil
}
