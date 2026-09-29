package ai

// Ports packages/ai/src/api/pi-messages.ts (readPiMessagesEvents, parsePiMessagesEvent, and the `for await` in stream() that consumes them).
//
// Pi runs the provider, its event queue, and the consumer on one JavaScript thread, so the assistant message a consumer observes at
// a delivery depends on how many microtasks the provider spends between two pushes. The generator, its yields and the consumer's
// for-await are built from the shared JavaScript primitives (jsAsyncGenerator, jsAwait), so their reactions are the ones ECMA-262
// defines; each await is cited at its source line. Nothing else in the loop depends on timing.

import (
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
		buffer := ""                      // pi-messages.ts:282
		// pi-messages.ts:283 stream.getReader(); the finally at :309 only releases the lock, which needs no action.
		for { // pi-messages.ts:285 while (true)
			chunk, err := loop.reader.ReadChunk() // pi-messages.ts:286 await reader.read(); ReadChunk models its microtasks.
			done := errors.Is(err, io.EOF)
			if err != nil && !done {
				return nil, err // The rejection throws out of the generator, which rejects the pending next().
			}
			if done {
				buffer += decoder.flush() // pi-messages.ts:287 decoder.decode()
			} else {
				buffer += decoder.decode(chunk) // pi-messages.ts:287 decoder.decode(value, { stream: true })
			}
			buffer = strings.ReplaceAll(buffer, "\r\n", "\n") // pi-messages.ts:288

			for { // pi-messages.ts:290-298
				split := strings.Index(buffer, "\n\n")
				if split == -1 {
					break
				}
				if err := emitPiMessagesEvent(body, buffer[:split]); err != nil {
					return nil, err
				}
				buffer = buffer[split+2:]
			}
			if done { // pi-messages.ts:300
				break
			}
		}
		if trimJSWhitespace(buffer) != "" { // pi-messages.ts:305
			if err := emitPiMessagesEvent(body, buffer); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

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
	var value any
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		return nil, false, err
	}
	switch value := value.(type) {
	case nil:
		return nil, false, nil
	case bool:
		return json.RawMessage(data), value, nil
	case float64:
		return json.RawMessage(data), value != 0, nil
	case string:
		return json.RawMessage(data), value != "", nil
	}
	return json.RawMessage(data), true, nil
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
