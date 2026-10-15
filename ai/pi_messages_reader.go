package ai

// Ports packages/ai/src/api/pi-messages.ts (readPiMessagesEvents, parsePiMessagesEvent, and the `for await` in stream() that consumes them).
//
// Pi runs the provider, its event queue, and the consumer on one JavaScript thread, so the assistant message a consumer observes at
// a delivery depends on how many microtasks the provider spends between two pushes. The generator, its yields and the consumer's
// for-await are built from the shared JavaScript primitives (jsAsyncGenerator, jsAwait), so their reactions are the ones ECMA-262
// defines; each await is cited at its source line. Nothing else in the loop depends on timing.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// piMessagesEventLoop is `for await (const piEvent of readPiMessagesEvents(response.body))` (pi-messages.ts:432-441) on the executor
// turn that runs the provider's async function.
//
// readPiMessagesEvents is a native async generator, so its yield, return and rejection reactions come from jsAsyncGenerator
// (ECMA-262 27.6.3) and the loop's awaits from jsAwait; nothing here counts microtasks. The body's reads are the one await the
// generator makes on a settled or pending Web Stream read, and ReadChunk models it.
type piMessagesEventLoop struct {
	// reader is `reader.read()` on the response body's ReadableStreamDefaultReader (pi-messages.ts:286), including the await of its
	// promise. A chunk is one value the stream delivers; io.EOF with no data is `{ done: true }`.
	reader interface{ ReadChunk() ([]byte, error) }
	turn   *continuationTurn
}

// errPiMessagesEventsEnded is the generator finishing without the consumer returning (pi-messages.ts:443, its message is added by the caller).
var errPiMessagesEventsEnded = errors.New("stream ended without a terminal event")

// events is `readPiMessagesEvents(stream)` (pi-messages.ts:279-312), created suspended at its start.
func (loop *piMessagesEventLoop) events() *jsAsyncGenerator[json.RawMessage] {
	return newJSAsyncGenerator(loop.turn.executor, func(body *jsGeneratorBody[json.RawMessage]) (json.RawMessage, error) {
		var decoder piMessagesTextDecoder // pi-messages.ts:280 new TextDecoder()
		// buffer is Pi's `buffer` (pi-messages.ts:282) after its CRLF replace. No "\n\n" starts before scanned. crlf is whether buffer
		// holds a "\r\n": the replace leaves one where a line feed follows two carriage returns, and Pi's next replace over the whole
		// buffer folds it. Without one, the replace changes only the new text and a carriage return that ends the buffer, so a record
		// that spans many reads is appended to and searched once.
		var buffer []byte
		scanned := 0
		crlf := false
		// pi-messages.ts:283 stream.getReader(); the finally at :309 only releases the lock, which needs no action.
		for { // pi-messages.ts:285 while (true)
			chunk, err := loop.reader.ReadChunk() // pi-messages.ts:286 await reader.read(); ReadChunk models its microtasks.
			done := errors.Is(err, io.EOF)
			if err != nil && !done {
				return nil, err // The rejection throws out of the generator, which rejects the pending next().
			}
			var text string
			if done {
				text = decoder.flush() // pi-messages.ts:287 decoder.decode()
			} else {
				text = decoder.decode(chunk) // pi-messages.ts:287 decoder.decode(value, { stream: true })
			}
			// pi-messages.ts:288 buffer = buffer.replace(/\r\n/g, "\n")
			if crlf {
				buffer = bytes.ReplaceAll(append(buffer, text...), piMessagesCRLF, piMessagesLF)
				scanned = 0
				crlf = bytes.Contains(buffer, piMessagesCRLF)
			} else {
				if text != "" && text[0] == '\n' && len(buffer) > 0 && buffer[len(buffer)-1] == '\r' {
					buffer = buffer[:len(buffer)-1]
				}
				joined := max(0, len(buffer)-1)
				scanned = min(scanned, joined)
				buffer = append(buffer, strings.ReplaceAll(text, "\r\n", "\n")...)
				crlf = bytes.Contains(buffer[joined:], piMessagesCRLF)
			}

			start := 0
			for { // pi-messages.ts:290-298
				split := bytes.Index(buffer[scanned:], piMessagesLFLF)
				if split == -1 {
					break
				}
				split += scanned
				if err := emitPiMessagesEvent(body, string(buffer[start:split])); err != nil {
					return nil, err
				}
				start = split + 2
				scanned = start
			}
			if start > 0 {
				buffer = append(buffer[:0], buffer[start:]...)
				crlf = crlf && bytes.Contains(buffer, piMessagesCRLF)
			}
			scanned = max(0, len(buffer)-1)
			if done { // pi-messages.ts:300
				break
			}
		}
		if rest := string(buffer); trimJSWhitespace(rest) != "" { // pi-messages.ts:305
			if err := emitPiMessagesEvent(body, rest); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

var (
	piMessagesCRLF = []byte("\r\n")
	piMessagesLF   = []byte("\n")
	piMessagesLFLF = []byte("\n\n")
)

// emitPiMessagesEvent is `const event = parsePiMessagesEvent(raw); if (event) { yield event; }` (pi-messages.ts:292-296, 305-309). A parse error throws out of
// the generator. A return() at the yield arrives as the jsGeneratorReturnCompletion the caller propagates.
func emitPiMessagesEvent(body *jsGeneratorBody[json.RawMessage], raw string) error {
	event, ok, err := parsePiMessagesEvent(raw)
	if err != nil || !ok {
		return err
	}
	return body.yield(jsValue(event)) // pi-messages.ts:294 `yield event`
}

// run drives the generator and hands each event to consume, which reports whether it was terminal. It is the `for await` at
// pi-messages.ts:432-441: each iteration awaits next(); a terminal event or a consumer error leaves the loop, and
// AsyncIteratorClose (ECMA-262 7.4.13) calls return() and awaits it before the original completion continues.
// It returns nil after a terminal event, errPiMessagesEventsEnded when the generator finishes first, and the rejection or consumer error otherwise.
func (loop *piMessagesEventLoop) run(consume func(raw json.RawMessage) (terminal bool, err error)) error {
	generator := loop.events()
	defer generator.dispose()
	for {
		next, err := jsAwait(loop.turn, jsPromiseOperand(generator.next()))
		if err != nil {
			return err
		}
		if next.done {
			return errPiMessagesEventsEnded
		}
		terminal, err := consume(next.value)
		if err == nil && !terminal {
			continue
		}
		_, closeErr := jsAwait(loop.turn, jsPromiseOperand(generator.returnWith(jsValue(json.RawMessage(nil)))))
		if err == nil {
			err = closeErr
		}
		return err
	}
}

// upstream: ai/src/api/pi-messages.ts:parsePiMessagesEvent
//
// parsePiMessagesEvent mirrors pi-messages.ts:314-323: the first line starting with "data:", trimmed, is JSON unless it is empty
// or [DONE]. A parsed value that is falsy is no event.
func parsePiMessagesEvent(raw string) (json.RawMessage, bool, error) {
	var data string
	found := false
	for line := range strings.SplitSeq(raw, "\n") {
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data, found = trimJSWhitespace(rest), true
			break
		}
	}
	if !found || data == "" || data == "[DONE]" {
		return nil, false, nil
	}
	if !json.Valid([]byte(data)) {
		if err := jsonparse.Validate([]byte(data)); err != nil {
			return nil, false, err
		}
		return nil, false, fmt.Errorf("invalid pi-messages event JSON: %s", data)
	}
	if strings.HasPrefix(data, "n") {
		return nil, false, nil // null
	}
	truthy, err := jsonTruthy([]byte(data))
	if err != nil {
		return nil, false, err
	}
	return json.RawMessage(data), truthy, nil
}

// jsonTruthy is whether JSON.parse(text) is truthy in JavaScript: false for null, false, 0, -0 and "", true for every other value. text is valid JSON with no leading blank. It reads only the top-level value, so an object or array is not built.
func jsonTruthy(text []byte) (bool, error) {
	// data is trimmed of JavaScript whitespace, and valid JSON starts with a value.
	switch text[0] {
	case 'n':
		return false, nil
	case 't':
		return true, nil
	case 'f':
		return false, nil
	case '"':
		var value string
		if err := json.Unmarshal(text, &value); err != nil {
			return false, err
		}
		return value != "", nil
	case '{', '[':
		return true, nil
	}
	var value float64
	if err := json.Unmarshal(text, &value); err != nil {
		return false, err
	}
	return value != 0, nil
}

// piMessagesTextDecoder is `new TextDecoder()` used with decode(chunk, { stream: true }) and a final decode(): a multi-byte
// sequence split across chunks is held back, each maximal malformed subsequence becomes one U+FFFD, and one leading BOM is dropped.
type piMessagesTextDecoder struct {
	pending []byte
	started bool
}

func (decoder *piMessagesTextDecoder) decode(chunk []byte) string {
	data := slices.Concat(decoder.pending, chunk)
	decoder.pending = nil
	if hold := incompleteUTF8Suffix(data); hold > 0 {
		decoder.pending = append([]byte(nil), data[len(data)-hold:]...)
		data = data[:len(data)-hold]
	}
	return decoder.finish(data)
}

func (decoder *piMessagesTextDecoder) flush() string {
	data := decoder.pending
	decoder.pending = nil
	return decoder.finish(data)
}

func (decoder *piMessagesTextDecoder) finish(data []byte) string {
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

// incompleteUTF8Suffix returns the length of a trailing byte run that is a proper prefix of a well-formed sequence.
func incompleteUTF8Suffix(data []byte) int {
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
