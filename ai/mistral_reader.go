//go:build !pig_strip_mistral_conversations

package ai

// Ports packages/ai/src/api/mistral-conversations.ts (readMistralEvents, findMistralEventBoundary, parseMistralEvent, and the `for await` in consumeChatStream that consumes them).
//
// Pi runs the provider, its event queue, and the consumer on one JavaScript thread, so the assistant message a consumer observes at a delivery depends on how many microtasks the provider spends between two pushes. Each await, yield and rejection that costs a microtask on that path is a suspendContinuation here, cited at its source line. Nothing else in the loop depends on timing.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// mistralChunkReader is `reader.read()` on the response body's ReadableStreamDefaultReader (mistral-conversations.ts:455). A chunk is one value the stream delivers; io.EOF with no data is `{ done: true }`. A body whose reads are observed models the microtasks of the read inside ReadChunk.
type mistralChunkReader interface {
	ReadChunk() ([]byte, error)
	// streamSettled reports whether the ReadableStream is closed or errored, the states in which reader.cancel() returns an already settled promise.
	streamSettled() bool
}

// mistralNativeReader reads an observed HTTP body through the Web Streams reader model.
type mistralNativeReader struct {
	*nativeBodyIterator
	firstRead bool
}

// ReadChunk is `await reader.read()`. Node 24.19.0's fetch body is a byte stream behind fetchFinale's pipeThrough, so the first read settles one generation later than a read of a plain ReadableStream: measured as 2 generations after the call for a chunk that arrived with the headers (providers/mistral-conversations/realticks.json, providers/google-generative-ai/undici.json). The delay is spent before the read is issued, which is equivalent for a read that waits on the socket.
func (reader *mistralNativeReader) ReadChunk() ([]byte, error) {
	if !reader.firstRead {
		reader.firstRead = true
		suspendContinuation(reader.turn)
	}
	return reader.nativeBodyIterator.ReadChunk()
}

func (reader *mistralNativeReader) streamSettled() bool {
	return reader.reader == nil || reader.reader.stream == nil || reader.reader.stream.state != webStreamReadable
}

// mistralIOReader delivers the chunks of a body whose reads are not observed.
type mistralIOReader struct {
	body    io.Reader
	buffer  []byte
	settled bool
}

func (reader *mistralIOReader) ReadChunk() ([]byte, error) {
	if reader.buffer == nil {
		reader.buffer = make([]byte, bufio.MaxScanTokenSize)
	}
	for {
		count, err := reader.body.Read(reader.buffer)
		if count > 0 {
			return append([]byte(nil), reader.buffer[:count]...), nil
		}
		if err != nil {
			reader.settled = true
			return nil, err
		}
	}
}

func (reader *mistralIOReader) streamSettled() bool { return reader.settled }

// mistralEventLoop is one run of `for await (const event of mistralStream)` over readMistralEvents (mistral-conversations.ts:440-484, 589). A nil turn models no microtasks: the body's reads are not observed, so their order is not reproducible.
type mistralEventLoop struct {
	reader mistralChunkReader
	turn   *continuationTurn
	// aborted returns `signal.reason` once the request's signal (options.signal combined with the timeout) has aborted.
	aborted func() error
	// publish makes the message state at the end of a synchronous segment visible to consumers. Pi's consumers share the provider's message object, so a mutation is observable at the next microtask even when no event was pushed.
	publish func()
}

func (loop *mistralEventLoop) tick() {
	if loop.turn != nil {
		if loop.publish != nil {
			loop.publish()
		}
		suspendContinuation(loop.turn)
	}
}

// finally is the generator's `finally` block (mistral-conversations.ts:475-483): `await reader.cancel()` on a stream that is already closed or errored is an await of a settled promise. A stream that is still readable cancels through ReadableStreamCancel, whose promise is `sourceCancelPromise.then(() => {})`, so the await takes one more microtask (Node lib/internal/webstreams/readablestream.js readableStreamCancel).
func (loop *mistralEventLoop) finally() {
	loop.tick()
	if !loop.reader.streamSettled() {
		loop.tick()
	}
}

// run drives readMistralEvents and hands each event to consume. It returns nil when the generator completes, and the error a rejection carries: from the generator (a read, the abort signal, JSON.parse or event validation) or from consume, whose error leaves the for-await through AsyncIteratorClose.
func (loop *mistralEventLoop) run(consume func(raw json.RawMessage) error) error {
	var decoder mistralTextDecoder
	buffer := ""
	for { // mistral-conversations.ts:453 while (true)
		if loop.aborted != nil {
			if reason := loop.aborted(); reason != nil { // :454 if (signal.aborted) throw signal.reason
				return loop.throw(reason)
			}
		}
		if loop.turn != nil && loop.publish != nil {
			loop.publish()
		}
		chunk, err := loop.reader.ReadChunk() // :455 await reader.read(); ReadChunk models its microtasks.
		done := errors.Is(err, io.EOF)
		if loop.aborted != nil {
			// :456 if (signal.aborted) throw signal.reason. An abort ends the body with the signal's reason: fetch rejects the read with it, and the abort listener's reader.cancel() (:448) settles a pending read as done.
			if reason := loop.aborted(); reason != nil {
				return loop.throw(reason)
			}
		}
		if err != nil && !done {
			return loop.throw(err)
		}
		if done {
			buffer += decoder.flush() // :457 decoder.decode()
		} else {
			buffer += decoder.decode(chunk) // :457 decoder.decode(value, { stream: true })
		}
		for { // :459-466
			index, length := findMistralEventBoundary(buffer)
			if index < 0 {
				break
			}
			event, isDone, err := parseMistralEvent(buffer[:index])
			if err != nil {
				return loop.throw(err) // :461 JSON.parse or the shape check throws inside the generator.
			}
			buffer = buffer[index+length:]
			if isDone { // :463 if (event === MISTRAL_STREAM_DONE) return
				return loop.complete()
			}
			if event != nil { // :464 if (event) yield event
				if err := loop.yield(event, consume); err != nil {
					return err
				}
			}
		}
		if done { // :468
			break
		}
	}
	if trimJSWhitespace(buffer) != "" { // :471
		event, isDone, err := parseMistralEvent(buffer)
		if err != nil {
			return loop.throw(err) // :472
		}
		if !isDone && event != nil { // :473 if (event !== MISTRAL_STREAM_DONE && event) yield event
			if err := loop.yield(event, consume); err != nil {
				return err
			}
		}
	}
	return loop.complete()
}

// yield is `yield event` and the consumer's loop body (mistral-conversations.ts:464, 473, 589). AsyncGeneratorYield awaits the yielded value (ECMA-262 27.6.3.8) and then resolves the pending next() promise; the consumer's for-await resumes one microtask after that.
func (loop *mistralEventLoop) yield(event json.RawMessage, consume func(json.RawMessage) error) error {
	loop.tick()
	loop.tick()
	err := consume(event)
	if err == nil {
		return nil
	}
	// The body throws inside the for-await: AsyncIteratorClose calls return() and awaits its result. The suspended generator resumes with a return completion, which AsyncGeneratorYield awaits (ECMA-262 27.6.3.8 step 5.b), runs its `finally` (:475-483), and completes; the consumer's await of the returned promise resumes one microtask after that.
	loop.tick()
	loop.finally()
	loop.tick()
	return err
}

// complete is the generator finishing: its `finally` runs, then the consumer's await of next() resolves with done.
func (loop *mistralEventLoop) complete() error {
	loop.finally()
	loop.tick()
	return nil
}

// throw is the generator throwing: its `finally` runs, then the consumer's await of next() rejects one microtask later.
func (loop *mistralEventLoop) throw(err error) error {
	loop.finally()
	loop.tick()
	return err
}

// findMistralEventBoundary is mistral-conversations.ts:486 (`/\r\n\r\n|\r\n\r|\r\n\n|\r\r\n|\n\r\n|\r\r|\n\r|\n\n/u.exec(buffer)`): the leftmost match, and at one position the first alternative that matches. index is -1 when the buffer has no boundary.
func findMistralEventBoundary(buffer string) (index, length int) {
	for i := 0; i < len(buffer); i++ {
		rest := buffer[i:]
		switch buffer[i] {
		case '\r':
			switch {
			case strings.HasPrefix(rest, "\r\n\r\n"):
				return i, 4
			case strings.HasPrefix(rest, "\r\n\r"), strings.HasPrefix(rest, "\r\n\n"), strings.HasPrefix(rest, "\r\r\n"):
				return i, 3
			case strings.HasPrefix(rest, "\r\r"):
				return i, 2
			}
		case '\n':
			switch {
			case strings.HasPrefix(rest, "\n\r\n"):
				return i, 3
			case strings.HasPrefix(rest, "\n\r"), strings.HasPrefix(rest, "\n\n"):
				return i, 2
			}
		}
	}
	return -1, 0
}

// parseMistralEvent is mistral-conversations.ts:491-506: the `data:` lines of one record, each with its leading whitespace trimmed, joined by "\n" and trimmed, are JSON unless they are empty or [DONE]. event is nil for a record with no data.
func parseMistralEvent(raw string) (event json.RawMessage, done bool, err error) {
	var data []string
	for _, line := range splitMistralLines(raw) {
		// upstream: packages/ai/src/api/mistral-conversations.ts:parseMistralEvent
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimLeft(rest, mistralJSWhitespace))
		}
	}
	text := strings.Trim(strings.Join(data, "\n"), mistralJSWhitespace)
	if text == "" {
		return nil, false, nil
	}
	if text == "[DONE]" {
		return nil, true, nil
	}
	if !json.Valid([]byte(text)) {
		if err := jsonparse.Validate([]byte(text)); err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("invalid Mistral streaming event JSON: %s", text)
	}
	var record map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &record) != nil || !strings.HasPrefix(string(record["choices"]), "[") {
		return nil, false, errors.New("Invalid Mistral streaming event")
	}
	return json.RawMessage(text), false, nil
}

// splitMistralLines is `raw.split(/\r\n|\r|\n/u)`.
func splitMistralLines(raw string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\r':
			lines = append(lines, raw[start:i])
			if i+1 < len(raw) && raw[i+1] == '\n' {
				i++
			}
			start = i + 1
		case '\n':
			lines = append(lines, raw[start:i])
			start = i + 1
		}
	}
	return append(lines, raw[start:])
}

// mistralTextDecoder is `new TextDecoder()` used with decode(chunk, { stream: true }) and a final decode(): a multi-byte sequence split across chunks is held back, each maximal malformed subsequence becomes one U+FFFD, and one leading BOM is dropped.
type mistralTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *mistralTextDecoder) decode(chunk []byte) string {
	data := append(append([]byte(nil), decoder.pending...), chunk...)
	decoder.pending = nil
	if hold := mistralIncompleteUTF8Suffix(data); hold > 0 {
		decoder.pending = append([]byte(nil), data[len(data)-hold:]...)
		data = data[:len(data)-hold]
	}
	return decoder.finish(data)
}

func (decoder *mistralTextDecoder) flush() string {
	data := decoder.pending
	decoder.pending = nil
	return decoder.finish(data)
}

func (decoder *mistralTextDecoder) finish(data []byte) string {
	var text strings.Builder
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			size = openAIInvalidUTF8Prefix(data)
		}
		if !decoder.started {
			decoder.started = true
			if r == '\uFEFF' {
				data = data[size:]
				continue
			}
		}
		text.WriteRune(r)
		data = data[size:]
	}
	return text.String()
}

// mistralIncompleteUTF8Suffix returns the length of a trailing byte run that is a proper prefix of a well-formed sequence.
func mistralIncompleteUTF8Suffix(data []byte) int {
	for hold := 1; hold < utf8.UTFMax && hold <= len(data); hold++ {
		lead := data[len(data)-hold]
		if !utf8.RuneStart(lead) {
			continue
		}
		if !utf8.FullRune(data[len(data)-hold:]) && openAIInvalidUTF8Prefix(data[len(data)-hold:]) == hold {
			return hold
		}
		return 0
	}
	return 0
}
