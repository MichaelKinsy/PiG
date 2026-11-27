package store

import (
	"bytes"
	"context"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/store/jsonl.ts
//
// Plain JSONL for archives (slice 6a §13): one part per line, UTF-8. The store yields and takes parts; this is the
// encoding it ships (others, compressed ones, come later without touching the applier).
//
// Async contract: Pi's `encodeJsonl`/`decodeJsonl` are async generators over AsyncIterables; here they are pull
// iterators (ByteSource, PartIterator, PartSource: `Next(ctx)` blocks for the next item, `Close` is the generator's
// `return`, which closes the source it wraps). Items come in source order, one source `Next` per chunk, nothing is
// read ahead; an error of the source or a malformed line is returned by the `Next` that reaches it and ends the
// iterator. Cancellation is the source's: `ctx` is passed to it.
//
// Go forced difference: a chunk of encoded lines is measured in bytes (Pi counts UTF-16 units); chunk boundaries are
// not part of the contract (decoding accepts chunks split anywhere).

// chunkBytes is the bytes a chunk of encoded lines collects before it is yielded.
const chunkBytes = 64 * 1024

// ByteSource is an AsyncIterable<Uint8Array>: Next gives the next chunk (ok false at the end), Close releases it
// early. Close may be called from another goroutine while a Next is in flight: the import does not wait for a stalled
// input. A chunk is valid until the next Next or Close: copy it to keep it. (Pi's streams hand out arrays of their own;
// a source here may fill the buffer it handed out again, as the sync bodies do.)
type ByteSource interface {
	Next(ctx context.Context) (chunk []byte, ok bool, err error)
	Close() error
}

type jsonlEncoder struct {
	parts PartIterator
	lines []byte
	done  bool
}

// EncodeJsonl is parts as JSONL bytes, in chunks of whole lines.
func EncodeJsonl(parts PartIterator) ByteSource { return &jsonlEncoder{parts: parts} }

func (encoder *jsonlEncoder) Next(ctx context.Context) ([]byte, bool, error) {
	if encoder.done {
		return nil, false, nil
	}
	for {
		part, ok, err := encoder.parts.Next(ctx)
		if err != nil {
			encoder.done = true
			return nil, false, err
		}
		if !ok {
			encoder.done = true
			if len(encoder.lines) > 0 {
				return encoder.take(), true, nil
			}
			return nil, false, nil
		}
		encoder.lines, err = jsstring.AppendMarshalJSON(encoder.lines, part)
		if err != nil {
			encoder.done = true
			return nil, false, err
		}
		encoder.lines = append(encoder.lines, '\n')
		if len(encoder.lines) >= chunkBytes {
			return encoder.take(), true, nil
		}
	}
}

func (encoder *jsonlEncoder) take() []byte {
	chunk := encoder.lines
	encoder.lines = nil
	return chunk
}

func (encoder *jsonlEncoder) Close() error {
	encoder.done = true
	return encoder.parts.Close()
}

type jsonlDecoder struct {
	bytes ByteSource
	// pending is the incomplete UTF-8 sequence the last chunk ended with.
	pending []byte
	// rest is the decoded UTF-8 bytes not yet split into lines; start is where the next line begins,
	// and bytes in rest[start:scanned] contain no '\n' (linear-time scanning, Pi da7e26cc8).
	rest    []byte
	start   int
	scanned int
	// line counts the lines taken so far.
	line     int
	started  bool
	finished atomic.Bool
}

// DecodeJsonl is the values of JSONL bytes, one per nonempty line, for ImportStore to check (a trust boundary: bytes
// that are no UTF-8 and a line that is no JSON are `invalid/args`).
func DecodeJsonl(bytes ByteSource) PartSource { return &jsonlDecoder{bytes: bytes} }

func (decoder *jsonlDecoder) parse(text string) (any, error) {
	value, err := delta.DecodeJson([]byte(text))
	if err != nil {
		decoder.finished.Store(true)
		return nil, data.Invalid(data.ReasonArgs, "jsonl: line "+strconv.Itoa(decoder.line)+" is not JSON")
	}
	return value, nil
}

func (decoder *jsonlDecoder) Next(ctx context.Context) (any, bool, error) {
	for !decoder.finished.Load() {
		if end := bytes.IndexByte(decoder.rest[decoder.scanned:], '\n'); end >= 0 {
			nl := decoder.scanned + end
			text := string(decoder.rest[decoder.start:nl])
			decoder.start = nl + 1
			decoder.scanned = decoder.start
			decoder.line++
			if len(jsstring.Trim(text)) > 0 {
				return decoder.emit(text)
			}
			continue
		}
		if decoder.start > 0 {
			n := copy(decoder.rest, decoder.rest[decoder.start:])
			decoder.rest = decoder.rest[:n]
			decoder.start = 0
		}
		decoder.scanned = len(decoder.rest)
		chunk, ok, err := decoder.bytes.Next(ctx)
		if err != nil {
			decoder.finished.Store(true)
			return nil, false, err
		}
		if ok {
			text, err := decoder.decode(chunk, false)
			if err != nil {
				return nil, false, err
			}
			decoder.rest = append(decoder.rest, text...)
			continue
		}
		text, err := decoder.decode(nil, true)
		if err != nil {
			return nil, false, err
		}
		decoder.rest = append(decoder.rest, text...)
		decoder.finished.Store(true)
		decoder.line++
		remainder := string(decoder.rest)
		decoder.rest = nil
		decoder.start, decoder.scanned = 0, 0
		if len(jsstring.Trim(remainder)) > 0 {
			return decoder.emit(remainder)
		}
	}
	return nil, false, nil
}

func (decoder *jsonlDecoder) emit(text string) (any, bool, error) {
	value, err := decoder.parse(text)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

// decode is TextDecoder("utf-8", { fatal: true }).decode(chunk, { stream: true }), and decode() when final: an
// ill-formed sequence fails `invalid/args` naming the line after those taken (Pi's `line + 1`); a sequence a chunk
// cuts is held for the next. A leading BOM is dropped, as TextDecoder does.
func (decoder *jsonlDecoder) decode(chunk []byte, final bool) (string, error) {
	buffer := slices.Concat(decoder.pending, chunk)
	complete := len(buffer)
	if !final {
		// Hold an incomplete trailing sequence: at most the last 3 bytes, starting at a lead byte.
		for back := 1; back <= 3 && back <= len(buffer); back++ {
			index := len(buffer) - back
			if utf8.RuneStart(buffer[index]) {
				if !utf8.FullRune(buffer[index:]) {
					complete = index
				}
				break
			}
		}
	}
	decoder.pending = append([]byte(nil), buffer[complete:]...)
	if !utf8.Valid(buffer[:complete]) {
		decoder.finished.Store(true)
		return "", data.Invalid(data.ReasonArgs, "jsonl: line "+strconv.Itoa(decoder.line+1)+" is not UTF-8")
	}
	text := string(buffer[:complete])
	if !decoder.started && len(text) > 0 {
		decoder.started = true
		text = strings.TrimPrefix(text, "\uFEFF")
	}
	return text, nil
}

func (decoder *jsonlDecoder) Close() error {
	decoder.finished.Store(true)
	return decoder.bytes.Close()
}
