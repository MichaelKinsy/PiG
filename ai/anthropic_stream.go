package ai

// Ports packages/ai/src/api/anthropic-messages.ts:iterateSseMessages, iterateAnthropicEvents and the for-await loop of stream().
//
// The Go code runs on the provider's executor turn. Each JavaScript await, yield and Promise adoption that separates the body reader from the provider's loop is a suspend(): one promise reaction queued behind everything already ready. The tick order is checked against Node 24.19 and 26.7 by TestAnthropicMicrotaskTrace over coding/testdata/rpc33-observation/providers/anthropic-messages/pi.json.

import (
	"bytes"
	"context"
	"encoding/json"
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
	// state.raw is cleared below, so the event owns it; copying it would only allocate.
	event := serverSentEvent{Event: state.event, Data: strings.Join(state.data, "\n"), Raw: state.raw}
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

// webTextDecoder is a TextDecoder with the default UTF-8 label and ignoreBOM false, called with {stream: true} per chunk and once without options at the end.
type webTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *webTextDecoder) decode(data []byte, stream bool) string {
	if text, ok := decoder.decodeValid(data, stream); ok {
		return string(text)
	}
	return string(decoder.decodeAppend(nil, data, stream))
}

// decodeTo appends the decoded text of data to dst.
func (decoder *webTextDecoder) decodeTo(dst, data []byte, stream bool) []byte {
	if text, ok := decoder.decodeValid(data, stream); ok {
		return append(dst, text...)
	}
	return decoder.decodeAppend(dst, data, stream)
}

// decodeValid decodes data when no sequence is pending and data is well-formed UTF-8, up to an incomplete sequence at its end that a streaming call keeps: the text is then data itself, without a leading byte order mark. ok is false for any other data, which decodeAppend decodes rune by rune.
func (decoder *webTextDecoder) decodeValid(data []byte, stream bool) (text []byte, ok bool) {
	if len(decoder.pending) > 0 {
		return nil, false
	}
	cut := len(data)
	if stream {
		cut -= incompleteUTF8Tail(data)
	}
	if !utf8.Valid(data[:cut]) {
		return nil, false
	}
	text = data[:cut]
	if cut < len(data) {
		decoder.pending = append([]byte(nil), data[cut:]...)
	}
	if cut > 0 && !decoder.started {
		decoder.started = true
		text = bytes.TrimPrefix(text, utf8BOM)
	}
	return text, true
}

// incompleteUTF8Tail is the length of the sequence at the end of data that is well-formed so far but incomplete.
func incompleteUTF8Tail(data []byte) int {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax+1; i-- {
		if data[i]&0xc0 != 0x80 {
			if utf8.FullRune(data[i:]) {
				return 0
			}
			return len(data) - i
		}
	}
	return 0
}

func (decoder *webTextDecoder) decodeAppend(text, data []byte, stream bool) []byte {
	data = append(decoder.pending, data...)
	decoder.pending = nil
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			if stream && !utf8.FullRune(data) {
				decoder.pending = append([]byte(nil), data...)
				break
			}
			// A maximal subpart of an ill-formed sequence becomes one replacement character.
			text = utf8.AppendRune(text, utf8.RuneError)
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
		text = utf8.AppendRune(text, r)
	}
	return text
}

// anthropicSSEReader is iterateSseMessages. Its events are computed when a chunk arrives and yielded one per next(); the generator's internal state has no observable effect between yields.
type anthropicSSEReader struct {
	ctx     context.Context
	source  openAIStreamChunkReader
	suspend func()

	decoder webTextDecoder
	state   anthropicSSEState
	// buffer is the decoded text after the last line consumed; its first scanned bytes hold no line end that can be consumed yet.
	buffer  []byte
	scanned int
	queue   []serverSentEvent
	readErr error
	ended   bool
	closed  bool
}

// anthropicLineBufferReuse is the largest line buffer the reader keeps once its lines are consumed.
const anthropicLineBufferReuse = 64 << 10

// anthropicConsumeLine is consumeLine. pig divergence (D121): a carriage return at the end of the buffer waits for the next chunk unless the stream has ended, because Pi ends the line there and the line feed that follows flushes the record without its data (pi-mono #10390).
func anthropicConsumeLine(text string, final bool) (line, rest string, ok bool) {
	index := strings.IndexAny(text, "\r\n")
	if index < 0 {
		return "", "", false
	}
	next := index + 1
	if !final && text[index] == '\r' && next == len(text) {
		return "", "", false
	}
	if text[index] == '\r' && next < len(text) && text[next] == '\n' {
		next++
	}
	return text[:index], text[next:], true
}

// drainLines applies consumeLine until it finds no line. The text up to the last line end that consumeLine would take becomes one string whose lines are substrings, so a line that spans many chunks is copied once and only the bytes of each new chunk are searched.
func (reader *anthropicSSEReader) drainLines(final bool) {
	buffer := reader.buffer
	search := buffer
	if !final && len(buffer) > 0 && buffer[len(buffer)-1] == '\r' {
		// anthropicConsumeLine does not take a carriage return that ends the buffer before the stream ends (D121).
		search = buffer[:len(buffer)-1]
	}
	if last := bytes.LastIndexAny(search[reader.scanned:], "\r\n"); last >= 0 {
		consumed := reader.scanned + last + 1
		segment := string(buffer[:consumed])
		if remainder := buffer[consumed:]; len(remainder) > 0 || cap(buffer) <= anthropicLineBufferReuse {
			reader.buffer = append(reader.buffer[:0], remainder...)
		} else {
			reader.buffer = nil
		}
		// Every line end in segment is decided: the byte after a carriage return inside it is known.
		for line, rest, ok := anthropicConsumeLine(segment, true); ok; line, rest, ok = anthropicConsumeLine(rest, true) {
			if event, ok := reader.state.decode(line); ok {
				reader.queue = append(reader.queue, event)
			}
		}
	}
	// What is left holds no line end, except a carriage return at its end that the next chunk decides.
	reader.scanned = len(reader.buffer)
	if reader.scanned > 0 && reader.buffer[reader.scanned-1] == '\r' {
		reader.scanned--
	}
	if !final {
		return
	}
	if len(reader.buffer) > 0 {
		if event, ok := reader.state.decode(string(reader.buffer)); ok {
			reader.queue = append(reader.queue, event)
		}
	}
	if event, ok := reader.state.flush(); ok {
		reader.queue = append(reader.queue, event)
	}
}

// next is one call of iterateSseMessages().next(): an event (its yield awaits the value), completion, or a thrown error.
//
//perf:hot
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
			reader.buffer = reader.decoder.decodeTo(reader.buffer, data, true)
			reader.drainLines(false)
		case errors.Is(err, io.EOF):
			reader.buffer = reader.decoder.decodeTo(reader.buffer, nil, false)
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

// anthropicStreamEvent is a parsed record of iterateAnthropicEvents: its SSE record, the JSON text that parsed (repaired when the original did not), and the parsed value's type, which every consumer dispatches on. Type is empty when the value has no string type member.
type anthropicStreamEvent struct {
	serverSentEvent
	JSON string
	Type string
	// valid reports that encoding/json accepts JSON (json.Valid); see anthropicParseJSON.
	valid bool
}

// next is one iteration of stream()'s `for await (const event of iterateAnthropicEvents(...))`: it includes the reactions from the generator chain and the loop's own await of the result (anthropic-messages.ts:601).
//
//perf:hot
func (reader *anthropicEventReader) next() (anthropicStreamEvent, bool, error) {
	event, ok, err := reader.generate()
	reader.suspend() // The provider's for-await awaits iterateAnthropicEvents().next().
	if err != nil || !ok {
		reader.finished = true
	}
	return event, ok, err
}

func (reader *anthropicEventReader) generate() (anthropicStreamEvent, bool, error) {
	if reader.finished {
		return anthropicStreamEvent{}, false, nil
	}
	for {
		sse, ok, err := reader.sse.next()
		reader.suspend() // `for await (const sse of iterateSseMessages(...))` awaits next() (anthropic-messages.ts:481).
		if err != nil {
			return anthropicStreamEvent{}, false, err // A throwing iterator is not closed.
		}
		if !ok {
			break
		}
		if sse.Event == "error" {
			reader.closeSSE()
			return anthropicStreamEvent{}, false, errors.New(sse.Data)
		}
		if !anthropicMessageEvents[sse.Event] {
			continue
		}
		text, valid, err := anthropicParseJSON(sse.Data)
		var eventType string
		if err == nil {
			eventType, err = anthropicRecordType(text)
		}
		if err != nil {
			reader.closeSSE()
			return anthropicStreamEvent{}, false, &anthropicSSEParseError{sse: sse, cause: err}
		}
		// The SSE event name only admits the record; the parsed value's type decides what it is (anthropic-messages.ts:552-556).
		switch eventType {
		case "message_start":
			reader.sawMessageStart = true
		case "message_stop":
			reader.sawMessageEnd = true
		}
		reader.suspend() // `yield event` awaits its operand (anthropic-messages.ts:497).
		return anthropicStreamEvent{serverSentEvent: sse, JSON: text, Type: eventType, valid: valid}, true, nil
	}
	if reader.sawMessageStart && !reader.sawMessageEnd {
		return anthropicStreamEvent{}, false, errors.New("Anthropic stream ended before message_stop")
	}
	return anthropicStreamEvent{}, false, nil
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

// anthropicRecordType is `event.type` for the provider's switch (anthropic-messages.ts:551-556): reading a member of null throws, and any other value gives anthropicEventType's answer.
func anthropicRecordType(text string) (string, error) {
	if strings.TrimSpace(text) == "null" {
		return "", errors.New("Cannot read properties of null (reading 'type')")
	}
	return anthropicEventType(text), nil
}

type anthropicSSEParseError struct {
	sse   serverSentEvent
	cause error
}

func (err *anthropicSSEParseError) Error() string {
	return "Could not parse Anthropic SSE event " + err.sse.Event + ": " + err.cause.Error() + "; data=" + err.sse.Data + "; raw=" + strings.Join(err.sse.Raw, `\n`)
}

func (err *anthropicSSEParseError) Unwrap() error { return err.cause }

// anthropicParseJSON is parseJsonWithRepair (packages/ai/src/utils/json-parse.ts:85-95): it returns the text that parses, repaired when the original does not, or V8's JSON.parse message for the text that was last tried. valid reports that encoding/json also accepts the returned text (json.Valid); JSON.parse accepts text it rejects, such as nesting deeper than its limit.
func anthropicParseJSON(data string) (text string, valid bool, err error) {
	if json.Valid([]byte(data)) {
		return data, true, nil
	}
	err = jsonparse.Validate([]byte(data))
	if err == nil {
		return data, false, nil
	}
	repaired := repairJSON(data)
	if repaired == data {
		return "", false, err
	}
	if json.Valid([]byte(repaired)) {
		return repaired, true, nil
	}
	if err := jsonparse.Validate([]byte(repaired)); err != nil {
		return "", false, err
	}
	return repaired, false, nil
}

// anthropicEventType is `event.type` of a parsed, non-null event when it is a string, and "" otherwise (anthropic-messages.ts:551-556). It walks the top-level members of the already validated JSON without allocating for an ordinary event: the member name is matched exactly and the last duplicate wins, as in JSON.parse (encoding/json would also match a case variant such as "TYPE").
func anthropicEventType(parsed string) string {
	i := anthropicSkipSpace(parsed, 0)
	if i >= len(parsed) || parsed[i] != '{' {
		return ""
	}
	value := ""
	for i = anthropicSkipSpace(parsed, i+1); i < len(parsed) && parsed[i] == '"'; {
		end, escaped := anthropicStringEnd(parsed, i)
		key := parsed[i+1 : end-1]
		if escaped {
			var decoded string
			if json.Unmarshal([]byte(parsed[i:end]), &decoded) != nil {
				return ""
			}
			key = decoded
		}
		i = anthropicSkipSpace(parsed, end)
		if i >= len(parsed) || parsed[i] != ':' {
			return ""
		}
		start := anthropicSkipSpace(parsed, i+1)
		i = anthropicValueEnd(parsed, start)
		if key == "type" {
			value = parsed[start:i]
		}
		i = anthropicSkipSpace(parsed, i)
		if i >= len(parsed) || parsed[i] != ',' {
			break
		}
		i = anthropicSkipSpace(parsed, i+1)
	}
	//portlint:allow utf16units the byte length only checks that the raw JSON token holds two quote bytes
	if len(value) < 2 || value[0] != '"' {
		return ""
	}
	if !strings.ContainsRune(value, '\\') {
		return value[1 : len(value)-1]
	}
	var eventType string
	_ = json.Unmarshal([]byte(value), &eventType)
	return eventType
}

func anthropicSkipSpace(data string, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	return i
}

// anthropicStringEnd returns the index after the string that starts at data[start], and whether it holds an escape.
func anthropicStringEnd(data string, start int) (int, bool) {
	escaped := false
	for i := start + 1; i < len(data); i++ {
		switch data[i] {
		case '\\':
			escaped = true
			i++
		case '"':
			return i + 1, escaped
		}
	}
	return len(data), escaped
}

// anthropicValueEnd returns the index after the JSON value that starts at data[start].
func anthropicValueEnd(data string, start int) int {
	depth := 0
	for i := start; i < len(data); i++ {
		switch c := data[i]; c {
		case '"':
			i, _ = anthropicStringEnd(data, i)
			if depth == 0 {
				return i
			}
			i--
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i
			}
			depth--
			if depth == 0 {
				return i + 1
			}
		case ',', ' ', '\t', '\n', '\r':
			if depth == 0 {
				return i
			}
		}
	}
	return len(data)
}

// webTextBytes is `await response.text()` over the body bytes (fetchBodyText). Well-formed text without a leading byte order mark is returned as is.
func webTextBytes(body []byte) []byte {
	if utf8.Valid(body) && !bytes.HasPrefix(body, utf8BOM) {
		return body
	}
	return []byte(fetchBodyText(body))
}

// readWebText reads a response body to its end as `response.text()` decodes it.
func readWebText(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(body)
	return webTextBytes(data), err
}
